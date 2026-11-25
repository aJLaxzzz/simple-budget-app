"""Small terminal presentation helpers; never captures password prompts."""
import os
import sys

LAST_STAGE='Подготовка операции'

COLORS={'green':32,'yellow':33,'red':31,'blue':34,'purple':35,'gray':90}

def verbose():return os.environ.get('VERBOSE')=='1'
def interactive():return sys.stdin.isatty() and sys.stdout.isatty()
def color(text,name):
    if not sys.stdout.isatty() or 'NO_COLOR' in os.environ or os.environ.get('TERM') in (None,'dumb'):return text
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
