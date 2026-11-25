#!/usr/bin/env python3
"""Offline transfer, deliberately separate from deployment and from stopping any app."""
import os
from pathlib import Path
import subprocess
import sys
import uuid
from common import Refused,require,atomic,load
from mac import LOCAL,local_binary

try:
    source=os.environ.get('LOCAL_DATA','')
    require(source and Path(source).is_absolute(),'LOCAL_DATA must name the actual legacy data directory explicitly')
    require(input('Приложение уже штатно остановлено с отдельного разрешения? Введите ОСТАНОВЛЕНО: ')=='ОСТАНОВЛЕНО','Source app must be stopped; this script never kills it')
    LOCAL.mkdir(mode=0o700,exist_ok=True)
    ident=uuid.uuid4().hex;repo=LOCAL/('transfer-'+ident);repo.mkdir(mode=0o700)
    binary=local_binary()
    p=subprocess.run([str(binary),'admin','legacy-export','--source',source,'--backup-dir',str(repo),'--installation-id',ident,'--confirm-stopped'],check=True,stdout=subprocess.PIPE)
    atomic(repo/'transfer-result.json',__import__('json').loads(p.stdout))
    result=load(repo/'transfer-result.json')
    print('Проверенный архив:',result['archive'])
    print('Installation ID:',ident)
    print('Исходные данные сохранены. Скопируйте архив также на независимый защищённый носитель.')
except (Refused,OSError,ValueError,subprocess.CalledProcessError) as e:
    print('Transfer refused: '+str(e),file=sys.stderr);sys.exit(1)
