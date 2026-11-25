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

def render(instance, identity, states, auth, sudo_mode):
    if ui.interactive() and os.environ.get('TERM') not in (None,'dumb'):print('\033[2J\033[H',end='')
    print(ui.color('Simple Budget — Server Manager','blue'))
    print(ui.color('Сервер '+IP,'gray'))
    for internal,label in (('production','PROD'),('acceptance','STG')):
        item=states.get(internal,{})
        state=item.get('service','unknown');revision=item.get('revision')
        text={'running':'работает','stopped':'остановлен'}.get(state,'состояние неизвестно')
        tone='green' if state=='running' else 'red' if state=='stopped' else 'gray'
        print(ui.color(label,'green' if internal=='production' else 'yellow')+'  '+ui.color(text,tone)+'  '+ui.color((revision[:12] if isinstance(revision,str) else 'коммит неизвестен'),'gray'))
    print('Выбрано: '+ui.color('PROD' if instance=='production' else 'STG','green' if instance=='production' else 'yellow')+' — ID '+identity)
    print(ui.color('AUTH='+auth+'; SUDO_MODE='+sudo_mode,'gray'))
    status=version_status.git(Path(__file__).resolve().parents[2],'status','--porcelain=v1','--untracked-files=normal')
    if status:print(ui.color('Локально есть незакоммиченные изменения. Сборка production-релиза будет запрещена.','yellow'))
    elif status is None:print(ui.color('Состояние локального Git неизвестно.','yellow'))
    groups=[('Приложение',[('1. Состояние сервера','blue'),('2. Обновить приложение','yellow'),('3. Просмотреть журналы','blue'),('7. Откатить обновление','yellow'),('13. Продолжить прерванную операцию','yellow')]),
            ('Резервные копии',[('4. Создать резервную копию','blue'),('5. Скачать резервную копию','blue'),('6. Восстановить данные','yellow')]),
            ('Безопасность',[('8. Управление устройствами','purple'),('9. Проверить HTTPS','purple'),('10. Создать PKI-бэкап','purple'),('11. Авторизация и Keychain','purple')]),
            ('Окружения',[('12. Выбрать PROD / STG','blue')])]
    for title,items in groups:
        print('\n'+ui.color(title,'gray'))
        for label,tone in items:print(ui.color(label,tone))
    state=states.get('acceptance',{}).get('service')
    label,tone=('14. Остановить STG','red') if state=='running' else ('14. Запустить STG','green') if state=='stopped' else ('14. STG: состояние неизвестно','gray')
    print(ui.color(label,tone))
    print(ui.color('15. Сбросить синтетические данные STG','yellow'))
    print(ui.color('16. Установить проверенный релиз STG в PROD','yellow'))
    print('0. Выход')

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
            state=Path.home()/'.simple-budget-admin'/(instance+'.json')
            identity=str(load(state).get('installation_id','неизвестен')) if state.exists() else 'не инициализирована'
            observation_error=None
            try:states=observe()
            except (Refused,OSError,ValueError,subprocess.SubprocessError) as e:
                states={};observation_error=str(e)
            render(instance,identity,states,auth,sudo_mode)
            if observation_error:print(ui.color(observation_error,'red'))
            pending=state.with_name('pending-'+instance+'.json')
            if pending.exists():print(ui.color('Есть незавершённая операция: сначала состояние (1), затем recovery (13).','yellow'))
            choice=input('Выбор: ').strip()
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
            if not action:print('Неизвестный пункт.');continue
            if action in ('auth-manual','auth-keychain'):
                auth=action.removeprefix('auth-');continue
            target_pending=Path.home()/'.simple-budget-admin'/('pending-'+target+'.json')
            if target_pending.exists() and action in ('deploy','promote','restore','rollback','stg-start','stg-stop','stg-reset'):
                print('Операция не запущена: сначала status/recover.');continue
            env=dict(os.environ,ENV='production',INSTANCE=target,AUTH=auth,SUDO_MODE=sudo_mode)
            for key in ('ARCHIVE','DECISION','BACKUP_ID','NAME','SECRET','ALLOW_CORRUPT_LIVE','KIND','LOCAL_DATA','PKI_ARCHIVE'):env.pop(key,None)
            env.update(extra)
            result=run([sys.executable,str(SCRIPT),action],env=env)
            print(ui.color('Операция завершена.' if result.returncode==0 else 'Операция завершилась с кодом '+str(result.returncode)+'. Проверьте причину выше; для прерванного изменения: состояние → recovery.','green' if result.returncode==0 else 'red'))
            if ui.interactive():input('Нажмите Enter для возврата в меню…')
        except EOFError:return 0
        except KeyboardInterrupt:print('\nОтменено.');return 130
        except (Refused,OSError,ValueError,subprocess.SubprocessError) as e:
            print(ui.color('Не выполнено: '+str(e),'red'))
            if ui.interactive():
                try:input('Нажмите Enter для возврата в меню…')
                except (EOFError,KeyboardInterrupt):return 130

if __name__=='__main__':sys.exit(main())
