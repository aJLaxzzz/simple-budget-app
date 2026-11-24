"""Build the offline Beta Update 1 evidence pack from real browser PNGs.
Run with Python + Pillow after the targeted Playwright screenshots.
"""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont
import hashlib, html, json, shutil, zipfile

root = Path('artifacts/beta-update-1')
font = ImageFont.truetype('/System/Library/Fonts/Supplemental/Arial.ttf', 24)
small = ImageFont.truetype('/System/Library/Fonts/Supplemental/Arial.ttf', 16)
scenarios = {
 'frequent-tasks': ('Три ближайших выполнения', 'Тот же поиск и повторение раз в два дня; полный список доступен отдельным действием.'),
 'recurring-cards': ('Оформление регулярных дел', 'Иконки, цвет источника, ближайшее подтверждённое выполнение и меню.'),
 'palette': ('Расширенная палитра', '12 → 26 цветов; произвольный HEX и системный color picker.'),
 'dental-icons': ('Поиск «зуб»', '319 → 321 иконка. Поиск по русским синонимам; собственные безопасные SVG.'),
 'task-menu': ('Порядок действий', 'Обычные действия сверху, опасные — после разделителя.'),
 'new-task': ('Глобальное создание', 'Компактная форма до изменений; полноценный редактор после, без предварительной записи в SQLite.'),
 'full-create-repeat': ('Создание с повторением', 'Новое состояние: оформление и повторение до первого атомарного сохранения.'),
 'series-appearance': ('Оформление всего повторения', 'Новое состояние: предпросмотр с защитой истории и индивидуальных свойств.'),
 'dynamic-tasks': ('Три настоящих динамических выполнения', 'Текущее выполнение и две зависимые задачи в SQLite. Предварительные даты явно подписаны.'),
 'dynamic-calendar': ('Динамическая дата в календаре', 'После переноса текущего выполнения будущая задача сохраняет ID, получает новую дату и остаётся видимой в календаре.'),
 'dynamic-decision': ('Завершение вне очереди', 'Явное решение: завершить выбранную будущую задачу, сохранив текущую опору. Вторая цепочка не создаётся.'),
 'project-existing': ('Добавление существующих задач', 'Поиск, множественный выбор и указание прежнего проекта. Перемещение без копирования.'),
 'series-project-move': ('Перемещение всего повторения', 'Предпросмотр незавершённых задач и обновление назначения будущих экземпляров. Закрытая история сохраняется.'),
 'permanent-birthdays': ('Постоянный проект с повторениями', 'Обычный проект содержит независимые ежегодные задачи. Ближайшие выполнения и полная история доступны отдельно.'),
 'weekly-cleaning': ('Уборка с относительными датами', 'Даты исходного дерева автоматически превращаются в смещения: суббота — начало, воскресенье — следующий день.')
}
for folder in ['comparisons', 'contact-sheets']:
 (root/folder).mkdir(exist_ok=True)
manifest=[]
for key,(title,note) in scenarios.items():
 for width in [1440,390]:
  name=f'{key}-{width}.png';after=root/'after'/name;before=root/'before'/name
  if not after.exists(): raise RuntimeError(f'Missing {after}')
  image=Image.open(after);assert image.size==(width,900)
  comparison=None
  if before.exists():
   old=Image.open(before);assert old.size==image.size
   pair=Image.new('RGB',(width*2+24,948),'#11161f');draw=ImageDraw.Draw(pair)
   draw.text((12,10),'ДО · '+title,font=font,fill='#dce4ed');draw.text((width+36,10),'ПОСЛЕ · '+title,font=font,fill='#dce4ed')
   pair.paste(old,(0,48));pair.paste(image,(width+24,48));comparison='comparisons/'+name;pair.save(root/comparison)
  manifest.append(dict(id=f'{key}-{width}',title=title,area=key,before='before/'+name if before.exists() else None,after='after/'+name,comparison=comparison,viewport=dict(width=width,height=900),result='Сценарий пройден в Playwright с настоящим API; см. результаты целевой визуальной проверки в отчёте.',notes=note if before.exists() else note+' Эквивалентного старого экрана нет; исходное изображение не подменяется.',sha256_after=hashlib.sha256(after.read_bytes()).hexdigest()))
(root/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n')
for page,start in enumerate(range(0,len(manifest),12),1):
 sheet=Image.new('RGB',(1440,840),'#11161f');draw=ImageDraw.Draw(sheet)
 for index,item in enumerate(manifest[start:start+12]):
  x=(index%4)*360;y=(index//4)*280
  pic=Image.open(root/(item['comparison'] or item['after'])).convert('RGB');pic.thumbnail((348,232))
  sheet.paste(pic,(x+(348-pic.width)//2,y+34));draw.text((x+6,y+5),f"{start+index+1}. {item['title'][:25]} · {item['viewport']['width']}",font=small,fill='#dce4ed')
 sheet.save(root/'contact-sheets'/f'sheet-{page:02}.png')
sections=[]
for key,(title,_) in scenarios.items():
 cards=[]
 for item in (m for m in manifest if m['area']==key):
  before=f'<a href="{item["before"]}">Оригинал до</a>' if item['before'] else '<span>Новый сценарий</span>'
  src=item['comparison'] or item['after']
  cards.append(f'<article><h3>{item["viewport"]["width"]} × 900</h3><p>{html.escape(item["notes"])}</p><a href="{src}"><img loading="lazy" src="{src}" alt="{html.escape(title)}"></a><p>{before} · <a href="{item["after"]}">Оригинал после</a></p></article>')
 sections.append(f'<section id="{key}"><h2>{html.escape(title)}</h2>'+''.join(cards)+'</section>')
sheets=' · '.join(f'<a href="contact-sheets/sheet-{i:02}.png">Контактный лист {i}</a>' for i in range(1,(len(manifest)+11)//12+1))
nav=' · '.join(f'<a href="#{k}">{html.escape(t)}</a>' for k,(t,_) in scenarios.items())
(root/'index.html').write_text('''<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Beta Update 1 — до и после</title><style>body{margin:24px auto;padding:0 20px;max-width:1500px;background:#10151d;color:#dae3ef;font:16px/1.5 system-ui}a{color:#87bcff}nav{padding:14px;background:#1b2430}section{margin:36px 0}article{margin:24px 0;border:1px solid #394655;border-radius:10px;padding:16px}img{display:block;max-width:100%;height:auto}h1,h2,h3{line-height:1.2}</style><h1>Beta Update 1 · целевая проверка</h1><p>Реальное приложение, Go API и изолированная SQLite. Слева — до, справа — после. Оригинальные PNG сохранены без уменьшения.</p><p><a href="BETA-UPDATE-1.md">Отчёт</a> · <a href="manifest.json">Манифест</a> · '''+sheets+'</p><nav>'+nav+'</nav>'+''.join(sections)+'</html>')
shutil.copyfile('docs/BETA-UPDATE-1.md',root/'BETA-UPDATE-1.md')
# Only explicit evidence types; never include DBs, user JSON, logs or executable builds.
files=[root/'index.html',root/'manifest.json',root/'BETA-UPDATE-1.md']
for folder in ['before','after','comparisons','contact-sheets']: files+=sorted((root/folder).glob('*.png'))
with zipfile.ZipFile('artifacts/beta-update-1.zip','w',zipfile.ZIP_DEFLATED) as z:
 for file in files:z.write(file,Path('beta-update-1')/file.relative_to(root))
with zipfile.ZipFile('artifacts/beta-update-1.zip') as z:
 assert z.testzip() is None
 names=set(z.namelist())
 for m in manifest:
  for k in ['before','after','comparison']:
   if m[k]:assert 'beta-update-1/'+m[k] in names
 assert all(Path(name).suffix in ['.png','.html','.json','.md'] for name in names)
print(json.dumps(dict(scenarios=len(manifest),originals=sum(1 for p in files if p.parent.name in ['before','after']),comparisons=sum(bool(m['comparison']) for m in manifest),archive_bytes=Path('artifacts/beta-update-1.zip').stat().st_size)))
