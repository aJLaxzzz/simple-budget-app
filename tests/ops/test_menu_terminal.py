import contextlib
import io
import os
import pty
import select
import fcntl
import signal
import struct
import termios
import time
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch
sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
import terminal_ui as ui
import server_manager as menu

class MenuTerminal(unittest.TestCase):
    def test_clear_only_supported_interactive_human_output(self):
        for tty,term,output,clear in [(True,'xterm-256color','',True),(True,'screen','',True),(False,'xterm','',False),(True,'dumb','',False),(True,'unknown','',False),(True,'xterm','json',False)]:
            with self.subTest(tty=tty,term=term,output=output),patch.dict(os.environ,{'TERM':term,'OUTPUT':output,'NO_COLOR':'1'}),patch.object(ui,'interactive',return_value=tty),contextlib.redirect_stdout(io.StringIO()) as out:
                ui.clear_menu()
                self.assertEqual(out.getvalue(),'\033[2J\033[H\033[3J' if clear else '')
                self.assertNotIn('1049',out.getvalue())
        with patch.dict(os.environ,{'TERM':'vt100','OUTPUT':''}),patch.object(ui,'interactive',return_value=True),contextlib.redirect_stdout(io.StringIO()) as out:
            ui.clear_menu()
            self.assertEqual(out.getvalue(),'\033[2J\033[H')
    def test_initial_clear_result_pause_then_redraw_success_and_error(self):
        for code in (0,1):
            with self.subTest(code=code),tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)),patch.dict(os.environ,{'TERM':'xterm','OUTPUT':'','INSTANCE':'','SUDO_MODE':''}),patch.object(ui,'interactive',return_value=True),patch.object(menu.version_status,'git',return_value=''),contextlib.redirect_stdout(io.StringIO()) as out:
                states={'production':{'service':'running','revision':'a'*40},'acceptance':{'service':'stopped','revision':'b'*40}}
                observe=Mock(return_value=states)
                def operation(*args,**kwargs):
                    self.assertTrue(out.getvalue().endswith('\033[3J'))
                    print('Ввод паролей и диагностический вывод операции')
                    return subprocess.CompletedProcess([],code)
                execute=Mock(side_effect=operation)
                choices=iter(['1','','0'])
                def answer(prompt):
                    if prompt.startswith('Нажмите Enter'):
                        screen=out.getvalue().rsplit('\033[3J',1)[-1]
                        self.assertNotIn('Server Manager',screen)
                        self.assertIn('диагностический вывод',screen)
                        self.assertIn('Операция завершена.' if code==0 else 'кодом 1',screen)
                    return next(choices)
                with patch('builtins.input',side_effect=answer):self.assertEqual(menu.main(run=execute,observe=observe),0)
                execute.assert_called_once();self.assertEqual(observe.call_count,2)
                self.assertNotIn('1049',out.getvalue())
                self.assertTrue(out.getvalue().endswith('\033[3J'))

    def test_compact_menu_fits_standard_terminal_and_keeps_all_commands(self):
        states={'production':{'service':'running','revision':'a'*40},'acceptance':{'service':'running','revision':'b'*40}}
        with patch.dict(os.environ,{'TERM':'xterm-256color','OUTPUT':'','NO_COLOR':'1'}),patch.object(ui,'interactive',return_value=True),patch.object(ui.shutil,'get_terminal_size',return_value=os.terminal_size((80,24))),patch.object(menu.version_status,'git',return_value=''),contextlib.redirect_stdout(io.StringIO()) as out:
            menu.render('production','a'*32,states,'manual','session')
            value=ui.ANSI.sub('',out.getvalue())
            self.assertLessEqual(len(value.splitlines())+1,24)
            self.assertTrue(all(len(line)<80 for line in value.splitlines()))
            for n in range(17):self.assertIn(str(n)+'. ',value)

    def test_interrupt_and_eof_exit_without_alternate_screen(self):
        for failure,expected in ((EOFError(),0),(KeyboardInterrupt(),130)):
            with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)),patch.dict(os.environ,{'TERM':'xterm','OUTPUT':'','INSTANCE':'','SUDO_MODE':''}),patch.object(ui,'interactive',return_value=True),patch.object(menu.version_status,'git',return_value=''),patch('builtins.input',side_effect=failure),contextlib.redirect_stdout(io.StringIO()) as out:
                self.assertEqual(menu.main(run=Mock(),observe=lambda:{}),expected)
                self.assertNotIn('1049',out.getvalue())
                if expected==0:self.assertTrue(out.getvalue().endswith('\033[3J'))
                else:self.assertIn('Отменено.',out.getvalue())

    def test_real_pty_logs_then_status_then_exit_show_only_current_screen(self):
        script="""
import getpass, subprocess, sys, termios, fcntl
fcntl.ioctl(0,termios.TIOCSCTTY,0)
sys.path.insert(0,sys.argv[1])
import server_manager as menu
menu.version_status.git=lambda *args:''
original=termios.tcgetattr(0)
def operation(args,**kwargs):
    action=args[-1]
    print('CURRENT_OPERATION='+action,flush=True)
    getpass.getpass('Синтетический пароль: ')
    print('RESULT='+action,flush=True)
    return subprocess.CompletedProcess([],0 if action=='logs' else 1)
states={'production':{'service':'running','revision':'a'*40},'acceptance':{'service':'running','revision':'b'*40}}
observations=0
def observe():
    global observations
    observations+=1
    return states
result=menu.main(run=operation,observe=observe)
assert observations==3
restored=termios.tcgetattr(0)
restored[3]&=~getattr(termios,'PENDIN',0)
original[3]&=~getattr(termios,'PENDIN',0)
assert restored==original
print('SHELL_READY',flush=True)
sys.exit(result)
"""
        with tempfile.TemporaryDirectory() as tmp:
            master,slave=pty.openpty()
            fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,80,0,0))
            env=dict(os.environ,HOME=tmp,TERM='xterm-256color',NO_COLOR='1',OUTPUT='',INSTANCE='production',SUDO_MODE='session',COLUMNS='80',LINES='24')
            process=subprocess.Popen([sys.executable,'-c',script,str(Path(menu.__file__).parent)],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True)
            os.close(slave);captured=b''
            def until(text,count=1):
                nonlocal captured
                deadline=time.monotonic()+10
                while captured.count(text.encode())<count:
                    self.assertLess(time.monotonic(),deadline,'PTY prompt timed out')
                    if select.select([master],[],[],.2)[0]:
                        chunk=os.read(master,65536)
                        self.assertTrue(chunk,'PTY closed before expected output')
                        captured+=chunk
            def screen():return ui.ANSI.sub('',captured.decode().rsplit('\033[3J',1)[-1]).strip()
            try:
                until('Выбор: ')
                self.assertTrue(captured.startswith(b'\033[2J\033[H\033[3J'))
                self.assertIn('Server Manager',screen())
                for count,(choice,action) in enumerate(((b'3\n','logs'),(b'1\n','status')),1):
                    os.write(master,choice);until('Синтетический пароль: ',count)
                    self.assertNotIn('Server Manager',screen())
                    self.assertIn('CURRENT_OPERATION='+action,screen())
                    self.assertNotIn('RESULT=',screen())
                    os.write(master,b'synthetic-private-input\n');until('Нажмите Enter',count)
                    self.assertIn('RESULT='+action,screen())
                    self.assertIn('Операция завершена.' if action=='logs' else 'кодом 1',screen())
                    if action=='status':self.assertNotIn('RESULT=logs',screen())
                    self.assertNotIn(b'synthetic-private-input',captured)
                    # Result stays until Enter; no asynchronous clearing.
                    self.assertFalse(select.select([master],[],[],.1)[0])
                    os.write(master,b'\n');until('Server Manager',count+1)
                    until(' 0. Выход',count+1)
                    self.assertNotIn('RESULT=',screen())
                    self.assertNotIn('CURRENT_OPERATION=',screen())
                    self.assertIn('Server Manager',screen())
                os.write(master,b'0\n');until('SHELL_READY')
                self.assertEqual(screen(),'SHELL_READY')
                self.assertEqual(process.wait(timeout=3),0)
                self.assertNotIn(b'1049',captured)
            finally:
                if process.poll() is None:process.terminate();process.wait(timeout=3)
                os.close(master)

    def test_adaptive_frames_keep_every_command_and_warning(self):
        states={'production':{'service':'running','revision':'a'*40},'acceptance':{'service':'stopped','revision':'b'*40}}
        for size in ((60,20),(80,24),(100,30),(120,40),(160,50)):
            for unicode in (True,False):
                for coloured in (True,False):
                    with self.subTest(size=size,unicode=unicode,coloured=coloured),patch.object(ui,'terminal_size',return_value=os.terminal_size(size)),patch.object(ui,'unicode_supported',return_value=unicode),patch.object(ui,'controls',return_value=coloured),patch.dict(os.environ,{'NO_COLOR':'1'} if not coloured else {},clear=True):
                        lines=menu.menu_lines('acceptance','i'*32,states,'manual','session','dirty',(('Есть незавершённая операция: сначала состояние (1), затем recovery (13).','yellow'),))
                        plain=ui.ANSI.sub('','\n'.join(lines))
                        self.assertTrue(all(ui.visible_width(line)<size[0] for line in lines))
                        for number in range(17):self.assertRegex(plain,r'(?m)(?:^| )'+str(number)+r'\. ')
                        self.assertIn('Выбрано: STG',plain)
                        self.assertIn('незакоммиченные изменения',plain)
                        self.assertIn('recovery (13)',plain)
                        self.assertIn('aaaaaaaaaaaa',plain);self.assertIn('bbbbbbbbbbbb',plain)
                        self.assertIn('Запустить STG',plain)
                        self.assertNotIn('\033[35m', '\n'.join(lines))
                        if not coloured:self.assertNotIn('\033',plain)
                        if not unicode:self.assertNotIn('─',plain);self.assertNotIn('╭',plain)
                        if size[0]==60:
                            self.assertFalse(any(' 1.' in row and ' 2.' in row for row in lines))
                        else:
                            self.assertTrue(any(' 1.' in row and ' 2.' in row for row in lines))
                        if size[0]>=120:
                            self.assertIn('',lines)
                            self.assertLessEqual(max(map(ui.visible_width,lines)),96)

    def test_width_and_wrap_with_ansi_cyrillic_and_unicode(self):
        self.assertEqual(ui.visible_width('\033[36mПривет\033[0m'),6)
        self.assertEqual(ui.visible_width('╭─│▸'),4)
        self.assertEqual(ui.visible_width('界e\u0301'),3)
        self.assertEqual(ui.visible_width(ui.pad('\033[32m界\033[0m',8)),8)
        text='Кириллица длинноеслово1234567890 e\u0301 界界'
        rows=ui.wrap(text,12)
        self.assertTrue(all(ui.visible_width(row)<=12 for row in rows))
        self.assertEqual(''.join(''.join(rows).split()),''.join(text.split()))

    def test_machine_and_redirected_output_have_no_control_sequences(self):
        for tty,output,term in ((True,'json','xterm'),(False,'','xterm'),(True,'','dumb')):
            with self.subTest(tty=tty,output=output,term=term),patch.dict(os.environ,{'TERM':term,'OUTPUT':output}),patch.object(ui,'interactive',return_value=tty),contextlib.redirect_stdout(io.StringIO()) as out:
                result=ui.menu_choice(lambda:menu.menu_lines('production','test',{},'manual','session'),lambda prompt:'0')
                self.assertEqual(result,'0');self.assertNotIn('\033',out.getvalue())

    def test_real_pty_resize_scroll_preserves_input_and_terminal(self):
        script="""
import sys, termios, signal, fcntl
fcntl.ioctl(0,termios.TIOCSCTTY,0)
sys.path.insert(0,sys.argv[1])
import terminal_ui as ui
import server_manager as menu
original=termios.tcgetattr(0);previous=signal.getsignal(signal.SIGWINCH)
states={'production':{'service':'running','revision':'a'*40},'acceptance':{'service':'running','revision':'b'*40}}
try:
    try:
        result=ui.menu_choice(lambda:menu.menu_lines('acceptance','test-installation',states,'manual','session','dirty'))
        print('CHOICE='+result)
    except KeyboardInterrupt:print('INTERRUPTED')
    except EOFError:print('EOF')
finally:
    restored=termios.tcgetattr(0)
    # macOS marks pending input for reprocessing when canonical mode returns.
    restored[3]&=~getattr(termios,'PENDIN',0)
    original[3]&=~getattr(termios,'PENDIN',0)
    print('RESTORED='+str(restored==original and signal.getsignal(signal.SIGWINCH)==previous))
"""
        for ending,expected in ((b'4\n','CHOICE=14'),(b'\x03','INTERRUPTED'),(b'\x15\x04','EOF')):
            with self.subTest(expected=expected),tempfile.TemporaryDirectory() as tmp:
                master,slave=pty.openpty()
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',20,60,0,0))
                env=dict(os.environ,HOME=tmp,TERM='xterm-256color',NO_COLOR='1',OUTPUT='',COLUMNS='200',LINES='100')
                process=subprocess.Popen([sys.executable,'-c',script,str(Path(menu.__file__).parent)],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True)
                os.close(slave);captured=b''
                def until(text,count=1):
                    nonlocal captured
                    deadline=time.monotonic()+10
                    while captured.count(text.encode())<count:
                        self.assertLess(time.monotonic(),deadline,'PTY resize timed out: '+repr(captured[-400:]))
                        if select.select([master],[],[],.2)[0]:captured+=os.read(master,65536)
                try:
                    until('Выбор: ')
                    self.assertIn('Строки 1–18/'.encode(),captured)
                    self.assertNotIn('16. Установить'.encode(),captured)
                    os.write(master,b'1');until('Выбор: 1')
                    os.write(master,b'\x1b[6~');until('16. Установить')
                    self.assertIn('Выбор: 1'.encode(),captured)
                    for columns,rows in ((120,40),(80,24),(160,50),(100,30),(60,20)):
                        before=captured.count(b'\033[2J')
                        fcntl.ioctl(master,termios.TIOCSWINSZ,struct.pack('HHHH',rows,columns,0,0))
                        os.kill(process.pid,signal.SIGWINCH)
                        until('\033[2J',before+1)
                    os.write(master,ending);until('RESTORED=True')
                    self.assertIn(expected.encode(),captured)
                    self.assertEqual(process.wait(timeout=3),0)
                    self.assertNotIn(b'1049',captured)
                    self.assertIn(b'\033[3J',captured)
                finally:
                    if process.poll() is None:process.terminate();process.wait(timeout=3)
                    os.close(master)

if __name__=='__main__':unittest.main()
