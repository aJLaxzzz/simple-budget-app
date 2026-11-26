"""Small terminal presentation helpers; never captures password prompts."""
import os
import sys
import shutil
import re
import unicodedata
import select
import signal
import termios
import codecs

LAST_STAGE='Подготовка операции'

COLORS={'green':32,'yellow':33,'red':31,'blue':36,'purple':35,'gray':90}

def verbose():return os.environ.get('VERBOSE')=='1'
def interactive():return sys.stdin.isatty() and sys.stdout.isatty()
def clear_menu():
    # Ordinary clear on the main screen. ED3 also clears saved lines on modern
    # terminals; older VT terminals receive only ED2 + cursor home.
    # NO_COLOR affects colour only; machine/redirected output is never cleared.
    if controls():
        scrollback='\033[3J' if os.environ.get('TERM','').startswith(('xterm','screen','tmux','rxvt')) else ''
        print('\033[2J\033[H'+scrollback,end='',flush=True)

ANSI=re.compile(r'\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))')

def controls():
    term=os.environ.get('TERM','')
    return interactive() and os.environ.get('OUTPUT')!='json' and (term in ('linux','ansi','vt100','vt220') or term.startswith(('xterm','screen','tmux','rxvt')))

def terminal_size():
    # A real resize must win over inherited COLUMNS/LINES.
    try:return os.get_terminal_size(sys.stdout.fileno())
    except (OSError,ValueError,AttributeError):return shutil.get_terminal_size((80,24))

def unicode_supported():
    try:'╭─│╰▸'.encode(sys.stdout.encoding or 'ascii')
    except (UnicodeError,AttributeError):return False
    return os.environ.get('TERM') not in ('dumb','unknown')

def visible_width(text):
    width=0
    for c in ANSI.sub('',text):
        if unicodedata.combining(c) or unicodedata.category(c) in ('Cf','Cc','Mn','Me'):continue
        width+=2 if unicodedata.east_asian_width(c) in ('W','F') else 1
    return width

def pad(text,width):return text+' '*max(0,width-visible_width(text))

def wrap(text,width):
    """Wrap plain labels before styling; never split ANSI sequences or drop words."""
    width=max(1,width);lines=[];line=''
    for word in text.split():
        if line and visible_width(line+' '+word)>width:lines.append(line);line=''
        if line:line+=' '
        for c in word:
            if line and visible_width(line+c)>width:lines.append(line);line=''
            line+=c
    if line or not lines:lines.append(line)
    return lines

def show_menu(lines,offset=0,typed='',cursor=None,redraw=True):
    width,height=terminal_size();width=max(1,width-1);height=max(3,height)
    scrolling=len(lines)+1>height
    count=max(1,height-(2 if scrolling else 1))
    offset=max(0,min(offset,max(0,len(lines)-count)))
    if redraw:
        clear_menu()
        for line in lines[offset:offset+count]:print(line)
    else:print('\r\033[2K',end='')
    if scrolling and redraw:
        hint=f'Строки {offset+1}–{min(len(lines),offset+count)}/{len(lines)} · ↑↓ / PgUp/PgDn'
        if not unicode_supported():hint=f'{offset+1}-{min(len(lines),offset+count)}/{len(lines)}  Up/Down / PgUp/PgDn'
        for line in wrap(hint,width)[:1]:print(color(line,'gray'))
    prompt='Выбор: ';cursor=len(typed) if cursor is None else cursor
    room=max(1,width-visible_width(prompt));left=cursor
    while left and visible_width(typed[left-1:cursor])<room:left-=1
    shown=typed[left:];end=len(shown)
    while visible_width(shown[:end])>room:end-=1
    shown=shown[:end]
    print(prompt+shown,end='',flush=True)
    back=visible_width(shown)-visible_width(typed[left:cursor])
    if back>0:print(f'\033[{back}D',end='',flush=True)
    return offset

def menu_choice(frame,input_fn=None):
    # Only the menu's number is edited here. Passwords always use ordinary input
    # outside this function, with the original terminal attributes restored.
    if input_fn is not None or not controls() or not sys.stdin.isatty():
        clear_menu()
        for line in frame():print(line)
        return (input_fn or input)('Выбор: ')
    fd=sys.stdin.fileno();original=termios.tcgetattr(fd)
    attrs=termios.tcgetattr(fd);attrs[3]&=~(termios.ECHO|termios.ICANON)
    attrs[6][termios.VMIN]=1;attrs[6][termios.VTIME]=0
    previous=signal.getsignal(signal.SIGWINCH);resized=False
    def winch(*_):
        nonlocal resized
        resized=True
    text='';cursor=0;offset=0;escape='';decoder=codecs.getincrementaldecoder('utf-8')('replace')
    try:
        signal.signal(signal.SIGWINCH,winch);termios.tcsetattr(fd,termios.TCSANOW,attrs)
        size=terminal_size();lines=frame();offset=show_menu(lines,offset,text,cursor)
        while True:
            if resized or terminal_size()!=size:
                resized=False;size=terminal_size();lines=frame();offset=show_menu(lines,offset,text,cursor)
            if not select.select([fd],[],[],.15)[0]:
                escape='';continue
            raw=os.read(fd,1)
            if not raw:raise EOFError
            char=decoder.decode(raw)
            if not char:continue
            old_offset=offset
            if char=='\x03':raise KeyboardInterrupt
            if char in ('\r','\n'):
                print();return text
            if char=='\x04':
                if not text:raise EOFError
                text=text[:cursor]+text[cursor+1:]
            elif char=='\x1b':escape=char;continue
            elif escape:
                escape+=char
                if escape in ('\x1b[','\x1bO'):continue
                if char.isdigit() or char==';':continue
                page=max(1,size.lines-3)
                if escape in ('\x1b[A','\x1bOA'):offset-=1
                elif escape in ('\x1b[B','\x1bOB'):offset+=1
                elif escape=='\x1b[5~':offset-=page
                elif escape=='\x1b[6~':offset+=page
                elif escape in ('\x1b[C','\x1bOC'):cursor=min(len(text),cursor+1)
                elif escape in ('\x1b[D','\x1bOD'):cursor=max(0,cursor-1)
                elif escape in ('\x1b[H','\x1bOH','\x1b[1~'):cursor=0
                elif escape in ('\x1b[F','\x1bOF','\x1b[4~'):cursor=len(text)
                elif escape=='\x1b[3~':text=text[:cursor]+text[cursor+1:]
                escape=''
            elif char in ('\x7f','\x08'):
                if cursor:text=text[:cursor-1]+text[cursor:];cursor-=1
            elif char=='\x15':text=text[cursor:];cursor=0
            elif char=='\x01':cursor=0
            elif char=='\x05':cursor=len(text)
            elif char.isprintable():text=text[:cursor]+char+text[cursor:];cursor+=1
            offset=show_menu(lines,offset,text,cursor,redraw=offset!=old_offset)
    finally:
        termios.tcsetattr(fd,termios.TCSANOW,original)
        signal.signal(signal.SIGWINCH,previous)

def pause_menu():
    if interactive():input('Нажмите Enter, чтобы вернуться в меню')

def color(text,name):
    if not controls() or 'NO_COLOR' in os.environ:return text
    return '\033['+str(COLORS[name])+'m'+text+'\033[0m'
def stage(text):
    global LAST_STAGE
    LAST_STAGE=text
    if os.environ.get('OUTPUT')!='json':print(color(text,'blue'),flush=True)

STAGES={'client-list':'Проверка устройств','client-add':'Добавление устройства','client-revoke':'Отзыв устройства','pki-export':'Проверка реестра сертификатов','tls-status':'Проверка HTTPS','logs':'Чтение журналов','rollback':'Откат обновления','stg-reset':'Подготовка и восстановление синтетического набора STG','prepare':'Установка обновления','promote':'Установка проверенного STG-релиза в PROD',
        'commit':'Проверка готовности и открытие доступа','backup':'Создание резервной копии',
        'status':'Проверка состояния сервера','fetch-backup':'Скачивание и проверка резервной копии',
        'restore':'Восстановление данных','recover':'Продолжение прерванной операции',
        'stg-setup':'Настройка STG и маршрута Caddy','stg-start':'Запуск STG','stg-stop':'Остановка STG'}
