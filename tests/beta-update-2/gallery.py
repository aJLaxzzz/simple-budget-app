"""Package only reproducible UX evidence; never traverse application data directories."""
from pathlib import Path
import json, html, hashlib, zipfile, math, shutil, textwrap
from PIL import Image, ImageDraw, ImageFont
root=Path('artifacts/beta-update-2')
for name in ['comparisons','contact-sheets']: (root/name).mkdir(parents=True,exist_ok=True)
font_path='/System/Library/Fonts/Supplemental/Arial.ttf'
font=ImageFont.truetype(font_path,20)
labels={
'tasks':'Общий список, ближайшие 30 дней и свёрнутая группа «Позже»',
'later-expanded':'Раскрытая группа «Позже»', 'annual-three':'Три настоящих ежегодных выполнения',
'frequent-three':'Три ближайших частых выполнения', 'future-filter':'Один фильтр: все выполнения',
'manual-skip-before':'Ручной пропуск: подтверждение решения','anchor-problem':'Отдельная проблема новой даты продолжения',
'manual-skip-after':'Ручной пропуск сразу обработан','disable-task':'Предпросмотр отключения повторения задачи',
'ordinary-after-disable':'Самостоятельная задача с прежним ID','quick-complete-undo':'Быстрое завершение и отмена',
'complete-other-date-menu':'Отдельное действие «Выполнить другой датой»','complete-other-date':'Выбор фактической даты',
'project':'Сортировка задач проекта по дате','batch-birthdays':'Предпросмотр независимых ежегодных повторений',
'permanent-birthdays':'Один обычный проект с независимыми днями рождения','nested-warning':'Защита от вложенных повторений',
'weekly-cleaning':'Еженедельная уборка: суббота и воскресенье','disable-project':'Отключение повторения проекта',
'project-after-disable':'Исходный проект и задачи после отключения','dynamic-completion':'Завершение динамической задачи и безопасная отмена',
'completion-network-error':'Ошибка сети: задача остаётся открытой'}
def area(stem):
 if stem in ['project','batch-birthdays','permanent-birthdays','nested-warning','weekly-cleaning','disable-project','project-after-disable']:return 'Проекты'
 if stem.startswith(('manual-skip','anchor-')):return 'Пропуски и внимание'
 if stem.startswith(('disable-task','ordinary-after')):return 'Отключение повторения'
 if stem.startswith(('quick-','complete-','completion-','dynamic-')):return 'Завершение'
 return 'Списки и группировка'
entries=[]
for p in sorted((root/'after').glob('*.png')):
 stem,width=p.stem.rsplit('-',1);w,h=Image.open(p).size
 entries.append(dict(id=p.stem,name=labels.get(stem,stem),area=area(stem),after=str(p.relative_to(root)),before=None,comparison=None,viewport={'width':w,'height':h},result='Проверено Playwright с Go API',notes='Изолированные данные. Контрольная дата 24.11.2026, Europe/Moscow.',sha256=hashlib.sha256(p.read_bytes()).hexdigest()))
for p in sorted((root/'before').glob('*.png')):
 new=root/'comparison-after'/p.name
 if not new.exists():continue
 before,after=Image.open(p).convert('RGB'),Image.open(new).convert('RGB')
 combined=Image.new('RGB',(before.width+after.width,max(before.height,after.height)+65),'#10151c');draw=ImageDraw.Draw(combined)
 draw.text((16,12),'ДО · Beta Update 1',font=font,fill='white');draw.text((before.width+16,12),'ПОСЛЕ · Beta Update 2',font=font,fill='white');combined.paste(before,(0,65));combined.paste(after,(before.width,65))
 out=root/'comparisons'/p.name;combined.save(out)
 stem,width=p.stem.rsplit('-',1)
 entries.append(dict(id='comparison-'+p.stem,name=labels[stem]+' · сопоставимые данные',area='До / после',before=str(p.relative_to(root)),after=str(new.relative_to(root)),comparison=str(out.relative_to(root)),viewport={'width':before.width,'height':before.height},result='Проверено',notes='Одинаковые запросы создания, названия, исходные даты и viewport. В новой версии больше реальных ежегодных выполнений — это результат исправления, а не добавленные вручную данные.',sha256=hashlib.sha256(new.read_bytes()).hexdigest()))
for offset in range(0,len(entries),12):
 page=entries[offset:offset+12];sheet=Image.new('RGB',(1200,math.ceil(len(page)/3)*330),'#151c25');d=ImageDraw.Draw(sheet)
 for i,e in enumerate(page):
  im=Image.open(root/(e['comparison'] or e['after'])).convert('RGB');im.thumbnail((380,270));x=(i%3)*400+10;y=(i//3)*330+10;sheet.paste(im,(x,y));d.text((x,y+273),'\n'.join(textwrap.wrap(f'{offset+i+1}. {e["name"]} · {e["viewport"]["width"]}',width=43)[:3]),font=ImageFont.truetype(font_path,15),fill='white')
 sheet.save(root/'contact-sheets'/f'sheet-{offset//12+1:02d}.png')
(root/'manifest.json').write_text(json.dumps({'date':'2026-11-24','scenarios':entries},ensure_ascii=False,indent=2))
esc=lambda x:html.escape(str(x),quote=True)
cards=[]
for e in entries:
 links=' '.join(f'<a href="{esc(e[k])}" target="_blank">{label}</a>' for k,label in [('before','До'),('after','После'),('comparison','Сравнение')] if e[k])
 cards.append(f'<article data-search="{esc(e["area"]+e["name"]+e["id"])}"><h2>{esc(e["name"])}</h2><p>{esc(e["area"])} · {esc(e["id"])} · {e["viewport"]["width"]} px</p><p>{links}</p><a href="{esc(e["comparison"] or e["after"])}" target="_blank"><img loading="lazy" src="{esc(e["comparison"] or e["after"])}" alt="{esc(e["name"])}"></a><p>{esc(e["notes"])}</p></article>')
(root/'index.html').write_text('''<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Beta Update 2 — целевая проверка</title><style>body{background:#10151c;color:#e4eaf1;font:16px system-ui;margin:24px}a{color:#84bbff;margin-right:12px}header{position:sticky;top:0;background:#10151c;padding:12px;z-index:2}input{padding:12px;width:min(85%,700px)}main{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,480px),1fr));gap:20px}article{border:1px solid #364356;border-radius:12px;padding:16px;min-width:0}img{width:100%;height:auto}h2{font-size:19px}p{line-height:1.5}</style><header><h1>Beta Update 2</h1><p><a href="BETA-UPDATE-2.md">Отчёт и обновление</a><a href="manifest.json">Манифест</a></p><input id="search" type="search" aria-label="Поиск сценариев" placeholder="Поиск: проект, пропуск, 390, comparison…"></header><main>'''+''.join(cards)+'''</main><script>document.getElementById('search').oninput=e=>{const q=e.target.value.toLocaleLowerCase('ru');document.querySelectorAll('article').forEach(a=>a.hidden=!a.dataset.search.toLocaleLowerCase('ru').includes(q));};</script></html>''')
report=Path('docs/BETA-UPDATE-2.md');shutil.copyfile(report,root/'BETA-UPDATE-2.md')
archive=Path('artifacts/beta-update-2.zip')
files=[root/'index.html',root/'manifest.json',root/'BETA-UPDATE-2.md']
for folder in ['before','after','comparison-after','comparisons','contact-sheets']:files+=list((root/folder).glob('*.png'))
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED)as z:
 for p in files:z.write(p,'beta-update-2/'+str(p.relative_to(root)))
with zipfile.ZipFile(archive)as z:assert z.testzip() is None
print(json.dumps({'scenarios':len(entries),'originals':sum(len(list((root/f).glob('*.png'))) for f in ['before','after','comparison-after']),'comparisons':len(list((root/'comparisons').glob('*.png'))),'sheets':len(list((root/'contact-sheets').glob('*.png'))),'archive_bytes':archive.stat().st_size}))
