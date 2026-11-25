"""Read-only local Git context for an exact server revision; never fetches."""
import os
import re
import subprocess


def git(root, *args):
    try:
        env=dict(os.environ, GIT_OPTIONAL_LOCKS='0')
        p=subprocess.run(['git','--no-replace-objects','-c','core.fsmonitor=false',*args],
                         cwd=root,env=env,capture_output=True,text=True,timeout=5)
        return p.stdout if p.returncode==0 else None
    except (OSError,subprocess.SubprocessError,UnicodeError):
        return None


def commit(root, revision):
    if not isinstance(revision,str) or not re.fullmatch('[0-9a-f]{40}',revision):
        return None
    raw=git(root,'show','-s','--format=%H%n%s%n%cI',revision+'^{commit}','--')
    if raw is None:return None
    lines=raw.splitlines()
    if len(lines)!=3 or lines[0]!=revision:return None
    # Commit subjects can contain terminal control characters.
    title=''.join(c for c in lines[1] if c.isprintable())[:240]
    return {'revision':revision,'title':title,'committed_at':lines[2],
            'metadata_source':'local_git_exact_object'}


def inspect(root, release):
    revision=release.get('revision')
    remote=commit(root,revision)
    head=git(root,'rev-parse','--verify','HEAD')
    head=head.strip() if head is not None else None
    local=commit(root,head)
    status=git(root,'status','--porcelain=v1','--untracked-files=normal')
    dirty=bool(status) if status is not None else None
    relation='unknown'
    if local and remote:
        if head==revision:relation='same_commit'
        else:
            counts=git(root,'rev-list','--left-right','--count',head+'...'+revision,'--')
            if counts and re.fullmatch(r'\s*\d+\s+\d+\s*',counts):
                ahead,behind=map(int,counts.split())
                relation='diverged' if ahead and behind else 'local_ahead' if ahead else 'server_ahead'
    return {'server_commit':remote,'local_commit':local,'local_head':head,
            'local_dirty':dirty,'relation':relation,
            'server_modified':release.get('modified')}


def display(info, release):
    revision=release.get('revision')
    short=revision[:12] if isinstance(revision,str) and re.fullmatch('[0-9a-f]{40}',revision) else '—'
    print('Версия VPS: '+short+'; схема: '+str(release.get('schema_version','—')))
    metadata=info['server_commit']
    if metadata:
        print('Коммит VPS: '+metadata['title']+'; дата коммита: '+metadata['committed_at']+' (локальный Git, точный SHA VPS)')
    else:print('Название и дата коммита VPS недоступны в локальном Git; метаданные не подставлены.')
    head=info['local_head']
    print('Локальный HEAD: '+(head[:12] if head else 'недоступен'))
    print('Незакоммиченные изменения: '+{True:'есть',False:'нет',None:'не удалось проверить'}[info['local_dirty']])
    labels={'same_commit':'одинаковый коммит','local_ahead':'локальный HEAD новее VPS по истории Git',
            'server_ahead':'VPS новее локального HEAD по истории Git',
            'diverged':'истории разошлись','unknown':'сравнение недоступно (не хватает Git-метаданных)'}
    print('Сравнение: '+labels[info['relation']]+'. Это сравнение коммитов; незакоммиченные изменения в него не входят.')
    if release.get('modified') is not False:
        print('Чистота серверной сборки не подтверждена; одинаковый SHA не доказывает совпадение исходников.')
