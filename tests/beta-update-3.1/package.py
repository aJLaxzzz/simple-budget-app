"""Small offline evidence set; originals come only from beta31.spec.cjs."""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont
import json,html
root=Path(__file__).resolve().parents[2]/'artifacts/beta-update-3.1'
manifest=json.loads((root/'manifest.json').read_text())
font=ImageFont.truetype('/Library/Fonts/Arial Unicode.ttf',24)
canvas=Image.new('RGB',(2880,950),'#141922');draw=ImageDraw.Draw(canvas)
for x,name,label in [(0,'calendar-before-1440.png','ДО: старые задачи сохранили прежние даты'),(1440,'calendar-after-1440.png','ПОСЛЕ: даты восстановлены по расписанию')]:
 im=Image.open(root/name);assert im.size==(1440,900);canvas.paste(im,(x,50));draw.text((x+18,12),label,font=font,fill='white')
canvas.save(root/'calendar-comparison.png')
labels={'calendar-before-1440.png':'До исправления: перенос начала повторения с сохранением дат задач','calendar-after-1440.png':'После исправления: пятница, суббота, воскресенье','calendar-deduplicated-1440.png':'После отдельного удаления дублирующего выполнения','repair-preview-1440.png':'Предпросмотр индивидуальных дат и совпадающего периода','repair-preview-390.png':'Предпросмотр при ширине 390 px','repair-noop-1440.png':'Повторная проверка: безопасных изменений нет','duplicate-confirmation-1440.png':'Отдельное подтверждение удаления одного выполнения'}
manifest['comparison']='calendar-comparison.png'
manifest['notes']='Одинаковые синтетические записи и декабрь 2026 года. После исправления часть событий вошла в видимый диапазон месяца; набор записей до удаления дубля не менялся. Пользовательские данные не включены.'
for f in manifest['screenshots']:assert (root/f).is_file()
(root/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
body=''.join(f'<section><h2>{html.escape(labels[n])}</h2><a href="{n}"><img src="{n}" loading="lazy"></a></section>' for n in manifest['screenshots'])
(root/'index.html').write_text('<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Beta Update 3.1</title><style>body{background:#141922;color:#e9edf2;font:16px system-ui;margin:24px}img{max-width:100%;height:auto}section{margin:32px 0}a{color:#74baff}h2{font-size:20px}</style><h1>Исправление дат повторяющихся проектов</h1><p>'+html.escape(manifest['notes'])+'</p><a href="calendar-comparison.png">Открыть сравнение до / после (2880 × 950)</a>'+body+'</html>')
print('Verified seven original PNGs, one comparison and offline gallery')
