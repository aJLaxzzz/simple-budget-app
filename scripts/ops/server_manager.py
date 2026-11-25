#!/usr/bin/env python3
"""A menu over mac.py, not a second deployment implementation."""
import os
from pathlib import Path
import subprocess
import sys
from common import *

SCRIPT=Path(__file__).with_name('mac.py')
MENU='''
1. Состояние сервера        2. Обновить приложение
3. Просмотреть журналы     4. Создать резервную копию
5. Скачать резервную копию 6. Восстановить данные
7. Откатить обновление     8. Управление устройствами
9. Проверить HTTPS        10. Создать PKI-бэкап
11. Авторизация и Keychain
12. Выбрать установку     13. Продолжить прерванную операцию
0. Выход'''
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

def main(input_fn=None,run=None):
    # Never inherit a production instance silently; each menu starts in acceptance.
    instance='acceptance';run=run or subprocess.run;auth=(os.environ.get('AUTH') or 'manual')
    while True:
        try:
            state=Path.home()/'.simple-budget-admin'/(instance+'.json')
            identity=load(state).get('installation_id','неизвестен') if state.exists() else 'не инициализирована'
            print('\nSimple Budget — budget / '+IP+' — INSTANCE='+instance+' — ID '+identity)
            print('AUTH='+auth+'; SUDO_MODE='+(os.environ.get('SUDO_MODE') or 'legacy'))
            pending=state.with_name('pending-'+instance+'.json')
            if pending.exists():print('Есть незавершённая операция. Сначала состояние (1), затем штатный recovery (13).')
            print(MENU);choice=input('Выбор: ').strip()
            if choice=='0':return 0
            if choice=='12':
                name=input('Введите acceptance или production: ').strip()
                require(name in ('acceptance','production'),'Неизвестная установка')
                if name=='production':require(input('Переключиться на production? Введите PRODUCTION: ')=='PRODUCTION','Отменено')
                instance=name;continue
            action,extra=selection(choice)
            if not action:print('Неизвестный пункт.');continue
            if action in ('auth-manual','auth-keychain'):
                auth=action.removeprefix('auth-');print('Выбран '+auth+'. Недоступный Keychain остановит команду без скрытого fallback.');continue
            if pending.exists() and action in ('deploy','restore','rollback'):
                print('Операция не запущена: сначала status/recover.');continue
            env=dict(os.environ,ENV='production',INSTANCE=instance,AUTH=auth)
            # No stale command arguments carried between menu actions.
            for key in ('ARCHIVE','DECISION','BACKUP_ID','NAME','SECRET','ALLOW_CORRUPT_LIVE','KIND','LOCAL_DATA','PKI_ARCHIVE'):env.pop(key,None)
            env.update(extra)
            result=run([sys.executable,str(SCRIPT),action],env=env)
            print('Готово.' if result.returncode==0 else 'Операция завершилась с кодом '+str(result.returncode)+'. Для прерванного изменения: состояние → recovery.')
        except EOFError:return 0
        except KeyboardInterrupt:print('\nОтменено. Секреты операции не сохраняются в меню.');return 130
        except (Refused,OSError,ValueError) as e:print('Не выполнено: '+str(e))

if __name__=='__main__':sys.exit(main())
