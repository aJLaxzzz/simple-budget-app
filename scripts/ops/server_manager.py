#!/usr/bin/env python3
"""A menu over mac.py, not a second deployment implementation."""
import os
from pathlib import Path
import subprocess
import sys
import terminal_ui as ui
import version_status
from common import *

SCRIPT=Path(__file__).with_name('mac.py')
ACTIONS={'1':'status','2':'deploy','3':'logs','4':'backup','5':'fetch-backup','6':'restore','7':'rollback','9':'tls-status','10':'pki-backup','13':'recover'}

def selection(choice):
    extra={}
    if choice=='8':
        print('1. Список  2. Выпустить  3. Добавить  4. Продлить  5. Отозвать')
        action={'1':'client-list','2':'client-issue','3':'client-add','4':'client-renew','5':'client-revoke'}.get(input('Выбор: ').strip())
        if action and action!='client-list':extra['DEVICE']=input('Имя устройства: ').strip()
        if action=='client-renew':extra['NAME']=input('Новое имя удостоверения: ').strip()
        return action,extra
    if choice=='11':
        print('1. Доступность Touch ID/Keychain  2. Сохранить/заменить секрет  3. Проверить секрет  4. Удалить секрет\n5. Ручной ввод  6. Использовать Keychain (только если доступен)')
        action={'1':'keychain-status','2':'keychain-register','3':'keychain-check','4':'keychain-delete','5':'auth-manual','6':'auth-keychain'}.get(input('Выбор: ').strip())
        if action and action not in ('keychain-status','auth-manual','auth-keychain'):
            extra['SECRET']=input('Назначение (app/client/pki): ').strip()
            if extra['SECRET']=='client':extra['DEVICE']=input('Имя устройства: ').strip()
        return action,extra
    action=ACTIONS.get(choice)
    if action=='restore':extra['ARCHIVE']=input('Полный путь архива: ').strip()
    if action=='recover':extra['DECISION']=input('Решение finish или rollback: ').strip()
    return action,extra

def overview():
    # Never prompt for sudo just to redraw the menu.
    if not (Path.home()/'.simple-budget-admin/production.json').exists():return {}
    env=dict(os.environ,ENV='production',OUTPUT='json')
    result=subprocess.run([sys.executable,str(SCRIPT),'overview'],env=env,capture_output=True,text=True,timeout=35)
    if result.returncode:raise Refused('SSH: состояния недоступны. '+result.stderr.strip()[-400:])
    return json.loads(result.stdout)

def menu_lines(instance, identity, states, auth, sudo_mode, local_status='', notices=()):
    columns,height=ui.terminal_size();width=max(12,min(columns-1,100))
    two=width>=77;gap=4;cell=min(46,(width-gap)//2) if two else width
    line_width=cell*2+gap if two else width
    unicode=ui.unicode_supported();rule='─' if unicode else '-'
    selected='PROD' if instance=='production' else 'STG'
    def styled_lines(text,tone='gray'):return [ui.color(line,tone) for line in ui.wrap(text,line_width)]
    def pair(left,right):
        return [ui.pad(left[i] if i<len(left) else '',cell)+' '*gap+(right[i] if i<len(right) else '') for i in range(max(len(left),len(right)))]
    def service(internal,label):
        item=states.get(internal,{})
        status=item.get('service','unknown');sha=item.get('revision')
        state={'running':'работает','stopped':'остановлен'}.get(status,'состояние неизвестно')
        tone={'running':'green','stopped':'red'}.get(status,'yellow')
        revision=sha[:12] if isinstance(sha,str) and sha else 'SHA неизвестен'
        chosen=internal==instance
        marker=('▸ ' if unicode else '> ') if chosen else '  '
        if two and height>=30 and unicode:
            name=label+(' · выбрано' if chosen else '')
            top='╭─ '+name+' '+rule*max(0,cell-ui.visible_width(name)-5)+'╮'
            return [ui.color(top,'blue' if chosen else 'gray'),
                    ui.color('│ ','gray')+ui.pad(ui.color(state,tone),cell-4)+ui.color(' │','gray'),
                    ui.color('│ ','gray')+ui.pad(ui.color(revision,'gray'),cell-4)+ui.color(' │','gray'),
                    ui.color('╰'+rule*(cell-2)+'╯','gray')]
        text=marker+label+'  '+state+' · '+revision
        # Unknown states wrap rather than truncate a diagnostic.
        return [ui.color(row,tone) for row in ui.wrap(text,cell)]
    header=[ui.color(row,'blue') for row in ui.wrap('Simple Budget — Server Manager',line_width)]
    header+=styled_lines('Сервер '+IP+(' · ' if unicode else ' | ')+'Выбрано: '+selected,'blue')
    prod,stg=service('production','PROD'),service('acceptance','STG')
    header+=pair(prod,stg) if two else prod+stg
    header+=styled_lines('ID '+identity)
    header+=styled_lines('AUTH='+auth+'; SUDO_MODE='+sudo_mode)
    if local_status:header+=styled_lines('Локально есть незакоммиченные изменения. Релиз требует коммита.','yellow')
    elif local_status is None:header+=styled_lines('Состояние локального Git неизвестно.','yellow')
    for message,tone in notices:header+=styled_lines(message,tone)
    state=states.get('acceptance',{}).get('service')
    toggle,tone=('Остановить STG','red') if state=='running' else ('Запустить STG','green') if state=='stopped' else ('STG: состояние неизвестно','yellow')
    groups=[('Приложение',[(1,'Состояние сервера','blue'),(2,'Обновить приложение','yellow'),(3,'Просмотреть журналы','blue'),(7,'Откатить обновление','yellow'),(13,'Продолжить прерванную операцию','yellow')]),
            ('Резервные копии',[(4,'Создать резервную копию','blue'),(5,'Скачать резервную копию','blue'),(6,'Восстановить данные','yellow')]),
            ('Безопасность',[(8,'Управление устройствами','blue'),(9,'Проверить HTTPS','blue'),(10,'Создать PKI-бэкап','blue'),(11,'Авторизация и Keychain','blue')]),
            ('Окружения',[(12,'Выбрать PROD / STG','blue'),(14,toggle,tone),(15,'Сбросить синтетические данные STG','yellow'),(16,'Установить проверенный релиз STG в PROD','yellow')])]
    def entry(item):
        number,label,tone=item;parts=ui.wrap(label,cell-4)
        return [(ui.color(f'{number:>2}. ',tone) if i==0 else '    ')+(ui.color(text,tone) if number==14 else text) for i,text in enumerate(parts)]
    blocks=[]
    for title,items in groups:
        heading=rule*2+' '+title+' '
        block=[ui.color(heading+rule*max(0,line_width-ui.visible_width(heading)),'gray')]
        for i in range(0,len(items),2 if two else 1):
            left=entry(items[i])
            block+=pair(left,entry(items[i+1])) if two and i+1<len(items) else left
        blocks.append(block)
    footer=[ui.color(' 0. ','blue')+'Выход']
    base=len(header)+sum(map(len,blocks))+len(footer)+1
    spacious=height>=base+len(blocks)+1
    lines=header
    for block in blocks:
        if spacious:lines.append('')
        lines+=block
    if spacious:lines.append('')
    return lines+footer

def render(instance, identity, states, auth, sudo_mode):
    status=version_status.git(Path(__file__).resolve().parents[2],'status','--porcelain=v1','--untracked-files=normal')
    ui.clear_menu()
    for line in menu_lines(instance,identity,states,auth,sudo_mode,status):print(line)

def main(input_fn=None,run=None,observe=None):
    instance=os.environ.get('INSTANCE') or 'production'
    sudo_mode=os.environ.get('SUDO_MODE') or 'session'
    if instance not in ('acceptance','production') or sudo_mode not in ('session','legacy'):
        print('Не выполнено: INSTANCE должен быть production/acceptance, SUDO_MODE — session/legacy.');return 2
    # Test callers supplying a fake executor never contact any real server.
    observe=observe or (overview if run is None else lambda:{})
    run=run or subprocess.run;auth=os.environ.get('AUTH') or 'manual'
    while True:
        try:
            ui.clear_menu()
            state=Path.home()/'.simple-budget-admin'/(instance+'.json')
            identity=str(load(state).get('installation_id','неизвестен')) if state.exists() else 'не инициализирована'
            observation_error=None
            try:states=observe()
            except (Refused,OSError,ValueError,subprocess.SubprocessError) as e:
                states={};observation_error=str(e)
            status=version_status.git(Path(__file__).resolve().parents[2],'status','--porcelain=v1','--untracked-files=normal')
            notices=[(observation_error,'red')] if observation_error else []
            pending=state.with_name('pending-'+instance+'.json')
            if pending.exists():notices.append(('Есть незавершённая операция: сначала состояние (1), затем recovery (13).','yellow'))
            # Resize redraws cached observations; it never invokes SSH or sudo.
            choice=ui.menu_choice(lambda:menu_lines(instance,identity,states,auth,sudo_mode,status,notices),input_fn).strip()
            ui.clear_menu()
            if choice=='0':return 0
            if choice=='12':
                name=input('Введите PROD или STG: ').strip().lower()
                name={'prod':'production','stg':'acceptance'}.get(name,name)
                require(name in ('production','acceptance'),'Неизвестное окружение')
                if name=='production':require(input('Переключиться на PROD? Введите PRODUCTION: ')=='PRODUCTION','Отменено')
                instance=name;continue
            target=instance
            if choice=='14':
                current=states.get('acceptance',{}).get('service')
                require(current in ('running','stopped'),'STG: состояние неизвестно. Проверьте SSH и обновите меню; переключение запрещено.')
                action='stg-stop' if current=='running' else 'stg-start';extra={};target='acceptance'
            elif choice=='15':action='stg-reset';extra={};target='acceptance'
            elif choice=='16':action='promote';extra={};target='production'
            else:action,extra=selection(choice)
            if not action:print('Неизвестный пункт.');ui.pause_menu();continue
            if action in ('auth-manual','auth-keychain'):
                auth=action.removeprefix('auth-');continue
            target_pending=Path.home()/'.simple-budget-admin'/('pending-'+target+'.json')
            if target_pending.exists() and action in ('deploy','promote','restore','rollback','stg-start','stg-stop','stg-reset'):
                print('Операция не запущена: сначала status/recover.');ui.pause_menu();continue
            env=dict(os.environ,ENV='production',INSTANCE=target,AUTH=auth,SUDO_MODE=sudo_mode)
            for key in ('ARCHIVE','DECISION','BACKUP_ID','NAME','SECRET','ALLOW_CORRUPT_LIVE','KIND','LOCAL_DATA','PKI_ARCHIVE'):env.pop(key,None)
            env.update(extra)
            result=run([sys.executable,str(SCRIPT),action],env=env)
            print(ui.color('Операция завершена.' if result.returncode==0 else 'Операция завершилась с кодом '+str(result.returncode)+'. Проверьте причину выше; для прерванного изменения: состояние → recovery.','green' if result.returncode==0 else 'red'))
            ui.pause_menu()
        except EOFError:ui.clear_menu();return 0
        except KeyboardInterrupt:print('\nОтменено.');return 130
        except (Refused,OSError,ValueError,subprocess.SubprocessError) as e:
            print(ui.color('Не выполнено: '+str(e),'red'))
            if ui.interactive():
                try:ui.pause_menu()
                except (EOFError,KeyboardInterrupt):return 130

if __name__=='__main__':sys.exit(main())
