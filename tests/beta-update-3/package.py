"""Assemble only curated screenshots and offline evidence. Requires Pillow to generate.
Run from repository root; no application/user data are read by this tool.
"""
from pathlib import Path
import json, html, zipfile, shutil, re
from PIL import Image, ImageDraw, ImageFont
base = Path('artifacts/beta-update-3')
font_path = next(p for p in [Path('/Library/Fonts/Arial Unicode.ttf'), Path('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf')] if p.exists())
font = ImageFont.truetype(str(font_path), 20)
small = ImageFont.truetype(str(font_path), 14)
for folder in ['comparisons', 'contact-sheets', 'after']:
    (base/folder).mkdir(parents=True, exist_ok=True)
for p in (base/'comparison-after').glob('*.png'):
    shutil.copyfile(p, base/'after'/p.name)
names = {
'birthdays': ('Постоянный проект: 11 дней рождения', 'Постоянные проекты', '33 реальные задачи сохранены. По умолчанию видны 11 ближайших; прогресс не считает будущие годы.'),
'cleaning-projects': ('Одна карточка повторения уборки', 'Повторяющиеся проекты', 'Сопоставимые периоды и задачи. После обновления новое повторение последовательно; до — прежние независимые карточки.'),
'cleaning-current': ('Текущее выполнение: 27–29 ноября', 'Повторяющиеся проекты', 'Исходный проект и три задачи с теми же плановыми датами.'),
'project-menu': ('Сгруппированное меню проекта', 'Меню и редактор', 'Частые действия сверху; редкие в раскрываемых группах, опасные отдельно.'),
'cleaning-calendar': ('Календарь: пятница, суббота, воскресенье', 'Даты', 'Декабрь 2026; те же состав и даты. Будущие последовательные выполнения обозначены предварительными.'),
'new-project': ('Полноценный редактор нового проекта', 'Меню и редактор', 'В обеих версиях введено только название «Подготовка к отпуску», сохранения ещё нет.'),
'manual-order': ('Ручная сортировка', 'Постоянные проекты', 'Перемещение за ручку и доступные кнопки вверх/вниз. Отдельного эквивалента в Beta Update 2 не было.'),
'cleaning-100-percent': ('100% до ручного завершения', 'Повторяющиеся проекты', 'Задачи завершены, проект остаётся открытым. Заметная кнопка завершения.'),
'cleaning-next': ('Следующее выполнение после завершения', 'Повторяющиеся проекты', 'Переход к уже созданному периоду 4–6 декабря без дублирования.'),
'cleaning-history': ('История предыдущих уборок', 'Повторяющиеся проекты', 'Закрытая уборка доступна из компактного раскрываемого списка.'),
'date-repair-preview': ('Предпросмотр исправления дат', 'Даты', 'Показаны изменения по выполнению и защищённые экземпляры. Данные не меняются до подтверждения.'),
'delete-current': ('Удаление только этого выполнения', 'Удаление', 'Область и число связанных записей проверены сервером; ничего не удалено ради снимка.'),
'delete-repeat': ('Удаление всего повторения', 'Удаление', 'Отдельная область удаления; внутренние снимки обрабатываются сервером. Подтверждение не нажималось ради снимка.')}
items=[]
for after in sorted((base/'after').glob('*.png')):
    match=re.fullmatch(r'(.+)-(1440|390)\.png', after.name)
    if not match: continue
    stem,width=match.groups(); width=int(width)
    title,category,note=names[stem]
    before=base/'before'/after.name
    item={'id':after.stem,'name':title,'category':category,'viewport':{'width':width,'height':900},'before':str(before.relative_to(base)) if before.exists() else None,'after':str(after.relative_to(base)),'comparison':None,'result':'Проверено в Chromium с настоящим Go API и временной SQLite','notes':note,'before_missing_reason':None if before.exists() else 'Новый сценарий либо не снимался до изменения. Эквивалентное изображение не подменяется другим состоянием.'}
    with Image.open(after) as ai:
        assert ai.size==(width,900), (after,ai.size)
        if before.exists():
            with Image.open(before) as bi:
                assert bi.size==ai.size
                out=Image.new('RGB',(width*2,960),'#101621');d=ImageDraw.Draw(out)
                d.text((16,8),'ДО · Beta Update 2',font=font,fill='white');d.text((width+16,8),'ПОСЛЕ · Beta Update 3',font=font,fill='white')
                d.text((16,35),title,font=small,fill='#c4cfdd');out.paste(bi,(0,60));out.paste(ai,(width,60))
                dest=base/'comparisons'/after.name;out.save(dest);item['comparison']=str(dest.relative_to(base))
    items.append(item)
# Contact sheets: 12 readable, numbered miniatures per page; full originals retained.
sheets=[]
for page,start in enumerate(range(0,len(items),12),1):
    sheet=Image.new('RGB',(1440,1110),'#101621');d=ImageDraw.Draw(sheet)
    for j,item in enumerate(items[start:start+12]):
        x=(j%4)*360;y=(j//4)*370
        with Image.open(base/item['after']) as im:
            im.thumbnail((344,310));sheet.paste(im,(x+(360-im.width)//2,y+8))
        label=f'{start+j+1:02d}. {item["name"]}'
        # Wrap caption without truncating scenario identity.
        words=label.split();line='';lines=[]
        for word in words:
            if d.textlength(line+' '+word,font=small)>342: lines.append(line);line=word
            else: line=(line+' '+word).strip()
        lines.append(line)
        for k,line in enumerate(lines[:2]):d.text((x+8,y+320+k*18),line,font=small,fill='white')
        d.text((x+8,y+355),str(item['viewport']['width'])+' px',font=small,fill='#a8c4e8')
    name=f'contact-sheets/after-{page:02d}.png';sheet.save(base/name);sheets.append(name)
paired=[i for i in items if i['comparison']]
for page,start in enumerate(range(0,len(paired),12),1):
    sheet=Image.new('RGB',(1440,750),'#101621');d=ImageDraw.Draw(sheet)
    for j,item in enumerate(paired[start:start+12]):
        x=(j%4)*360;y=(j//4)*250
        with Image.open(base/item['comparison']) as im:
            im.thumbnail((344,195));sheet.paste(im,(x+(360-im.width)//2,y+8))
        d.text((x+8,y+205),f'{start+j+1:02d}. '+item['id'],font=small,fill='white')
        d.text((x+8,y+226),'ДО | ПОСЛЕ',font=small,fill='#a8c4e8')
    name=f'contact-sheets/comparisons-{page:02d}.png';sheet.save(base/name);sheets.append(name)
manifest={'baseline_commit':'efd71cd','capture_date':'2026-11-24','locale':'ru-RU','timezone':'Europe/Moscow','screenshots_before':sum(bool(i['before']) for i in items),'screenshots_after':len(items),'comparisons':sum(bool(i['comparison']) for i in items),'contact_sheets':sheets,'scenarios':items}
(base/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
esc=html.escape
categories=list(dict.fromkeys(i['category'] for i in items))
nav=''.join(f'<a href="#cat-{n}">{esc(c)}</a>' for n,c in enumerate(categories))
sections=[]
for n,c in enumerate(categories):
    cards=[]
    for index,i in enumerate(items,1):
        if i['category']!=c:continue
        before=f'<a target="_blank" href="{i["before"]}"><img loading="lazy" src="{i["before"]}" alt="До: {esc(i["name"])}"><span>ДО</span></a>' if i['before'] else '<p>Снимок «до» отсутствует: '+esc(i['before_missing_reason'])+'</p>'
        after=f'<a target="_blank" href="{i["after"]}"><img loading="lazy" src="{i["after"]}" alt="После: {esc(i["name"])}"><span>ПОСЛЕ</span></a>'
        compare=f'<a href="{i["comparison"]}" target="_blank">Оригинал сравнения</a>' if i['comparison'] else ''
        cards.append(f'<article data-search="{esc(i["name"]+" "+i["notes"])}"><h3>{index:02d}. {esc(i["name"])} · {i["viewport"]["width"]} px</h3><p>{esc(i["notes"])}</p><div class="pair">{before}{after}</div>{compare}</article>')
    sections.append(f'<section id="cat-{n}"><h2>{esc(c)}</h2>'+''.join(cards)+'</section>')
head='''<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Beta Update 3 — до и после</title><style>body{margin:24px;background:#101621;color:#e5eaf2;font:16px/1.5 system-ui}a{color:#8cc6ff}nav{display:flex;gap:16px;flex-wrap:wrap}input,select{font:inherit;padding:8px;margin:12px 8px 12px 0;background:#1e293b;color:white;border:1px solid #68788f}article{padding:16px;margin:20px 0;border:1px solid #475569;border-radius:12px}.pair{display:grid;grid-template-columns:1fr 1fr;gap:12px}.pair img{width:100%;height:440px;object-fit:contain;object-position:top;background:#080d14}.pair span{display:block}.only-after .pair>:first-child{display:none}.only-after .pair{grid-template-columns:1fr}article[hidden]{display:none}@media(max-width:650px){body{margin:12px}.pair{grid-template-columns:1fr}.pair img{height:auto}}</style><h1>Beta Update 3 — целевая проверка</h1><p>Реальное приложение, изолированная SQLite. Данные и календарные периоды сопоставимых снимков одинаковы. Нажмите изображение для просмотра оригинала.</p>'''
links='<p><a href="BETA-UPDATE-3.md">Технический отчёт</a> · <a href="manifest.json">Манифест</a></p>'+''.join(f'<a href="{s}">Контактный лист {n+1}</a> ' for n,s in enumerate(sheets))
script='''<script>document.querySelector('#search').oninput=e=>document.querySelectorAll('article').forEach(a=>a.hidden=!a.dataset.search.toLowerCase().includes(e.target.value.toLowerCase()));document.querySelector('#view').onchange=e=>document.body.classList.toggle('only-after',e.target.value==='after');</script></html>'''
(base/'index.html').write_text(head+'<nav>'+nav+'</nav><label>Поиск <input id="search" type="search"></label><label>Просмотр <select id="view"><option value="pair">До / после рядом</option><option value="after">После</option></select></label>'+links+''.join(sections)+script)
shutil.copyfile('docs/BETA-UPDATE-3.md',base/'BETA-UPDATE-3.md')
allowed={'index.html','manifest.json','BETA-UPDATE-3.md',*sheets}
for i in items:
    allowed.update(i[k] for k in ('before','after','comparison') if i[k])
for link in re.findall(r'(?:href|src)="([^"]+)"',(base/'index.html').read_text()):
    if not link.startswith('#'): assert (base/link).is_file(),link
archive=base.with_suffix('.zip')
with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=6) as z:
    for name in sorted(allowed):z.write(base/name,'beta-update-3/'+name)
with zipfile.ZipFile(archive) as z:
    assert z.testzip() is None
    assert all(not n.endswith(('.db','.sqlite','.json-wal')) for n in z.namelist())
print(json.dumps({k:v for k,v in manifest.items() if k!='scenarios'},ensure_ascii=False))
print('ZIP bytes:',archive.stat().st_size)
