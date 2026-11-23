from pathlib import Path
import json,shutil,hashlib,struct
R=Path('artifacts/ux21-final-verification');old=Path('artifacts/final-ux-review')
(R/'before-screenshots').mkdir(exist_ok=True);(R/'before-after').mkdir(exist_ok=True);(R/'fix-verification').mkdir(exist_ok=True)
before=json.loads((old/'manifest.json').read_text());after={x['id']:x for x in json.loads((R/'captured.json').read_text())}
annotations=json.loads((R/'checks/comparison-notes.json').read_text()) if (R/'checks/comparison-notes.json').exists() else {}
manifest=[]
for b in before:
 a=after.get(b['id']);out={}
 for key,target in [('file','before_screenshot'),('full_page_file','before_full')]:
  if b.get(key):
   dest='before-screenshots/'+b[key].removeprefix('screenshots/');(R/dest).parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(old/b[key],R/dest);out[target]=dest
 adapted=b['id']=='template-create-unhandled-error'
 if adapted:
  file='after-screenshots/11-ux21-fixes/def01-unsaved-guard-1440.png'
  if (R/file).exists():a={'file':file,'steps':['Открыть проект из шаблона','Выбрать 30 ноября 2026','Нажать Escape','Проверить защиту черновика и продолжить редактирование'],'metrics':{},'viewport':{'width':1440,'height':900}}
 note=annotations.get(b['id'],{})
 out.update(original_id=b['id'],title=b['title'],category=b['category'],after_screenshot=a['file'] if a else None,coverage_status=('adapted' if adapted else 'reproduced') if a else 'not_reproduced',comparison_result=note.get('comparison','Ожидает визуальной оценки.' if a else 'Сравнение не выполнено.'),issues=note.get('issues',[]),steps=a.get('steps',[]) if a else b.get('steps',[]),before_steps=b.get('steps',[]),missing_reason=None if a else 'Сценарий не завершён при автоматическом обходе; подробности в checks/capture-gaps.json.',equivalence_note=note.get('equivalence','Те же контрольная дата, этап наполнения и пользовательское состояние. Внутренние ID созданы заново.' if not adapted else 'Повторено действие, приводившее к потере черновика; вместо необработанного исключения показана новая защита. Состояния по определению различаются.'),after_metrics=a.get('metrics',{}) if a else {},viewport=b.get('viewport',{'width':1440,'height':900}))
 if a and a.get('full_page_file'):out['after_full']=a['full_page_file']
 for field in ['before_screenshot','after_screenshot']:
  if out.get(field):out[field+'_sha256']=hashlib.sha256((R/out[field]).read_bytes()).hexdigest()
 manifest.append(out)
(R/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
ids={x['original_id']:x for x in manifest}
mapping={'DEF-01':('template-create-unhandled-error','Защита создания проекта из шаблона'),'UX-01':('tasks-twenty','Высота шапки'),'UX-02':('calendar-day-dense','Расписание и задачи без времени'),'UX-03':('nav-projects','Различимость повторяющихся проектов'),'UX-04':('task-editor-basic','Компактность редактора'),'UX-05':('nav-attention','Основное действие во внимании'),'UX-06':('task-cancel-confirm','Конкретное подтверждение'),'UX-07':('recurrence-rule-preview','Предпросмотр рядом с применением'),'UX-08':('series-completion','Понятное повторение от выполнения'),'UX-09':('project-partial-skipped','Честный прогресс с пропусками'),'UX-10':('nav-tasks','Убрана нерелевантная строка поиска'),'UX-11':('tasks-twenty','Читаемость подписей'),'UX-12':('icon-search-cleaning','Подписанные иконки'),'UX-13':('settings-imported-years','Разделение настроек'),'UX-14':('calendar-reset-filters','Сброс к значениям по умолчанию'),'UX-15':('task-work-periods','Устранение повторных заголовков'),'UX-16':('project-history','История при открытии'),'UX-17':('series-schedule','Нейтральные пояснения'),'UX-18':('responsive-tasks-390','Мобильная компоновка'),'UX-19':('project-template-instantiate','Именованные диалоги и фокус'),'UX-20':('work-period-completed-task-open','Контекст уведомлений')}
fixnotes=json.loads((R/'checks/fix-notes.json').read_text()) if (R/'checks/fix-notes.json').exists() else {}
fixes=[]
for k,(id,title) in mapping.items():
 x=ids[id]
 if not x['after_screenshot']:raise Exception('Missing fix evidence '+k+' '+id)
 notes=fixnotes.get(k,{})
 if k=='UX-03':x={**x,'before_screenshot':x.get('before_full',x['before_screenshot']),'after_screenshot':x.get('after_full',x['after_screenshot'])}
 fixes.append({'id':k,'title':title,'scenario_id':id,'before':x['before_screenshot'],'after':x['after_screenshot'],'pair':'before-after/'+k.lower()+'-before-after.png','result':notes.get('result','Ожидает независимой оценки'),'difference':notes.get('difference',x['comparison_result']),'residual':notes.get('residual','')})
(R/'fix-verification/matrix.json').write_text(json.dumps(fixes,ensure_ascii=False,indent=2))
listed={x.get(k) for x in manifest for k in ['after_screenshot','after_full'] if x.get(k)}
extra=[]
for p in sorted((R/'after-screenshots').rglob('*.png')):
 file=str(p.relative_to(R))
 if file not in listed:
  w,h=struct.unpack('>II',p.read_bytes()[16:24]);extra.append({'id':p.stem,'file':file,'width':w,'height':h,'description':'Дополнительная проверка UX 2.1; состав фикстуры отличается от исходного массового набора.'})
(R/'fix-verification/evidence.json').write_text(json.dumps(extra,ensure_ascii=False,indent=2))
print('Reproduced',sum(bool(x['after_screenshot']) for x in manifest),'/',len(manifest),'extra PNG',len(extra))
