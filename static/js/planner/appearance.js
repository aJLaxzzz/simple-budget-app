(function(P){
 'use strict';const {el,button,meta}=P;
 const palette=P.palette={slate:['Нейтральный','#9ba8bb'],blue:['Синий','#79aaff'],cyan:['Голубой','#62c9e3'],teal:['Бирюзовый','#65c6b3'],green:['Зелёный','#85c993'],lime:['Лаймовый','#b4c77a'],yellow:['Золотой','#dfc077'],orange:['Оранжевый','#e6ab77'],coral:['Коралловый','#e9998b'],rose:['Розовый','#dba0c4'],purple:['Лиловый','#bc9ee8'],indigo:['Индиго','#9ca8ed'],red:['Красный','#ef6464'],burgundy:['Бордовый','#ae506a'],pink:['Розовый яркий','#f179b1'],magenta:['Фуксия','#d66ce0'],amber:['Янтарный','#f5b937'],emerald:['Изумрудный','#36ba83'],mint:['Мятный','#b0ead5'],aqua:['Аквамарин','#35bfc4'],sky:['Небесный','#a8d8ff'],navy:['Лазурный','#5479da'],violet:['Фиолетовый','#9463dc'],brown:['Коричневый','#b58b70'],sand:['Песочный','#e0ccb0'],silver:['Серебристый','#d6dbe4']};
 P.colorName=id=>palette[id]?.[0]||id||'Наследование';
 P.colorValue=id=>palette[id]?.[1]||(/^#[0-9a-f]{6}$/i.test(id||'')?id:palette.slate[1]);
 P.timing=(task,today=P.today())=>!P.active(task)||task.dynamic?{key:'',label:''}:task.due_date&&task.due_date<today?{key:'overdue',label:'Просрочен крайний срок'}:task.planned_date&&task.planned_date<today?{key:'plan_missed',label:'Не выполнено по плану'}:{key:'',label:''};
 P.appearanceURL=id=>/^((?:lucide|app):[a-z0-9-]+|custom:[a-zA-Z0-9-]+)$/.test(id||'')?'/api/planner/icons/'+encodeURIComponent(id)+'/image':null;
 P.paint=(node,item)=>{node.style.setProperty('--pl-accent',P.colorValue(item.effective_color_id||item.color_id));node.classList.add('pl-personalized');return node;};
 P.appearanceIcon=(item,project=false)=>{
  const id=item.effective_icon_id||item.icon_id||(project?'lucide:folder':'lucide:circle-check');const url=P.appearanceURL(id);
  const icon=id.startsWith('custom:')&&url?el('img',{src:url+'?size=64',alt:'',class:'pl-appearance-icon pl-custom-icon',loading:'lazy'}):el('span',{class:'pl-appearance-icon','aria-hidden':'true'});
  if(!id.startsWith('custom:')&&url){icon.style.maskImage='url("'+url+'")';icon.style.webkitMaskImage='url("'+url+'")';}
  return P.paint(icon,item);
 };
 let catalogPromise;
 P.iconCatalog=(refresh=false)=>{if(refresh)catalogPromise=null;return catalogPromise||(catalogPromise=P.api('/icons').catch(e=>{catalogPromise=null;throw e;}));};
 // A local picker changes form controls only. The existing save/revision/dirty guard owns persistence.
 P.appearanceField=function(host,value={},project=false,context,prefix=''){
  const color=el('input',{type:'hidden',name:prefix+'color_id',value:value.color_id||''});const icon=el('input',{type:'hidden',name:prefix+'icon_id',value:value.icon_id||''});
  const details=el('div',{class:'pl-appearance-picker'}),summary=button('',chooseScope,'pl-appearance-trigger'),preview=el('span',{class:'pl-appearance-preview'}),hint=el('span',{},'Оформление');
  summary.setAttribute('aria-label','Оформление');summary.setAttribute('aria-haspopup','dialog');summary.append(preview,hint);details.append(summary,color,icon);host.append(details);
  const body=el('div',{class:'pl-appearance-body'});let loaded=false, catalog=[],opening;
  const read=()=>({color_id:color.value||null,icon_id:icon.value||null});
  function effective(){
   const place=context?context():{project:value.project_id,parent:value.parent_task_id};
   const parent=place.parentAppearance||P.data?.tasks?.find(t=>t.id===place.parent),p=place.projectAppearance||P.data?.projects?.find(t=>t.id===place.project);
   return {...read(),effective_color_id:color.value||(!project&&(parent?.effective_color_id||parent?.color_id||p?.effective_color_id||p?.color_id))||'slate',effective_icon_id:icon.value||(!project&&(parent?.effective_icon_id||parent?.icon_id||p?.effective_icon_id||p?.icon_id))||(project?'lucide:folder':'lucide:circle-check')};
  }
  function drawPreview(){const item=effective(),name=catalog.find(i=>i.id===item.effective_icon_id)?.name||'Стандартная иконка';preview.replaceChildren(P.appearanceIcon(item,project));hint.textContent=(color.value?(palette[color.value]?.[0]||color.value):project?'Нейтральное оформление':'Наследовать оформление')+' · '+name;if(body.selected)body.selected.replaceChildren(P.appearanceIcon(item,project),el('strong',{},name),meta('В черновике · сохраните карточку'));}
  function update(control,value){const form=host.closest('form');if(form?.dataset.busy||form?.dataset.uncertain)return;control.value=value;control.dispatchEvent(new Event('input',{bubbles:true}));drawPreview();body.querySelectorAll('[data-color]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.color===color.value)));body.querySelectorAll('[data-icon]').forEach(b=>{b.setAttribute('aria-pressed',String(b.dataset.icon===icon.value));P.paint(b.querySelector('.pl-appearance-icon'),effective());});}
  // Context controls may not exist until the caller has finished building the form.
  preview.append(P.appearanceIcon(value,project));
  async function chooseScope(){
   if(!value.series_id&&!value.repeat_series_id)return openPicker();
   const {dialog,close}=P.dialog('Область оформления');dialog.append(el('p',{},'Изменить оформление выбранного выполнения или всего повторения?'),el('div',{class:'pl-menu-items'},button('Только это выполнение',()=>{close(true);openPicker();}),button('Все незавершённые и будущие выполнения',async()=>{close(true);if(P.guardActive&&!await P.guardActive())return;P.editSeriesAppearance(value.series_id||value.repeat_series_id);})));
  }
  async function openPicker(){
   const form=host.closest('form');if(form?.dataset.busy||form?.dataset.uncertain)return;
   opening={color:color.value,icon:icon.value};
   const {dialog,close}=P.dialog('Оформление');dialog.classList.add('pl-appearance-dialog');
   const selected=el('div',{class:'pl-picker-selected',role:'status'});dialog.append(selected,body);
   const footer=el('div',{class:'pl-picker-footer'},button('Отменить выбор',()=>{update(color,opening.color);update(icon,opening.icon);close(true);}),button('Готово',()=>close(true),'pl-primary'));
   dialog.append(footer);dialog.addEventListener('close',()=>{summary.focus();});
   body.selected=selected;drawPreview();
   if(loaded){const picker=body.querySelector('input[type=color]'),hex=body.querySelector('[aria-label="HEX-цвет"]');if(picker)picker.value=P.colorValue(color.value);if(hex){hex.value=color.value.startsWith('#')?color.value:'';hex.setCustomValidity('');}body.querySelector('input[type=search]')?.focus();return;}loaded=true;
   body.append(el('p',{class:'pl-muted'},project?'Небольшой акцент для проекта.':'Пустые настройки наследуются от родителя и проекта.'));
   const colors=el('div',{class:'pl-color-grid','aria-label':'Цвет'});body.append(colors);
   colors.append(button(project?'Стандартный цвет':'Наследовать цвет',()=>update(color,''),'pl-color-reset'));
   for(const [id,[name,hex]]of Object.entries(palette)){const b=button('',()=>update(color,id),'pl-swatch');b.style.setProperty('--swatch',hex);b.setAttribute('aria-label',name);b.setAttribute('aria-pressed',String(color.value===id));b.dataset.color=id;colors.append(b);}
   const custom=el('div',{class:'pl-custom-color'}),picker=el('input',{type:'color',value:P.colorValue(color.value),'aria-label':'Произвольный цвет'}),hex=el('input',{type:'text',value:color.value.startsWith('#')?color.value:'',placeholder:'#RRGGBB',maxLength:7,'aria-label':'HEX-цвет',pattern:'#[0-9a-fA-F]{6}'});
   picker.oninput=()=>{hex.value=picker.value;hex.setCustomValidity('');update(color,picker.value);};hex.oninput=()=>{const valid=/^#[0-9a-f]{6}$/i.test(hex.value);hex.setCustomValidity(valid?'':'Введите # и шесть шестнадцатеричных цифр');if(valid){picker.value=hex.value;update(color,hex.value);}};custom.append(el('span',{},'Свой цвет'),picker,hex);colors.after(custom);
   const filters=el('div',{class:'pl-icon-filters'});const search=el('input',{type:'search',placeholder:'Поиск иконки','aria-label':'Поиск иконки'});const category=el('select',{'aria-label':'Категория иконок'});filters.append(search,category);body.append(filters);
   body.append(button(project?'Стандартная иконка':'Наследовать иконку',()=>update(icon,'')));
   const categoryName=el('p',{class:'pl-icon-category-name',role:'status'});body.append(categoryName);
   const icons=el('div',{class:'pl-icon-grid','aria-label':'Каталог иконок'});body.append(icons);
   // Search first; only one page of the catalog is mounted at a time.
   body.prepend(filters);
   const recent=el('div',{class:'pl-icon-recent'});const viewport=el('div',{class:'pl-picker-results',tabindex:0,'aria-label':'Результаты поиска иконок'});body.append(viewport);viewport.append(recent,categoryName,icons);
   const count=el('p',{class:'pl-muted',role:'status'}),more=button('Ещё иконки',()=>{limit+=48;draw();});icons.after(count,more);
   let items=[],limit=48;
   function choice(i){const b=button('',()=>{update(icon,i.id);P.rememberIcon(i.id);},'pl-icon-choice');b.dataset.icon=i.id;b.title=i.name;b.setAttribute('aria-label',i.name);b.setAttribute('aria-pressed',String(icon.value===i.id));b.append(P.appearanceIcon({...effective(),effective_icon_id:i.id}),el('span',{class:'pl-icon-caption'},i.name));return b;}
   function draw(){
    categoryName.textContent=category.value||'Все категории';
    const suggested=!search.value.trim()&&!category.value?P.iconSuggestions(items).slice(0,4):[];
    recent.replaceChildren();if(suggested.length)recent.append(meta('Недавние и популярные'),el('div',{class:'pl-icon-grid'},suggested.map(choice)));
    const found=P.iconSearch(items,search.value,category.value),visible=found.filter(i=>!suggested.some(s=>s.id===i.id));
    icons.replaceChildren(...visible.slice(0,limit).map(choice));
    if(!found.length)icons.append(meta('Иконки не найдены'));
    count.textContent=found.length?'Показано '+(Math.min(visible.length,limit)+suggested.length)+' из '+found.length:'';
    more.hidden=visible.length<=limit;
   }
   const load=async()=>{try{items=await P.iconCatalog();catalog=items;drawPreview();category.replaceChildren(el('option',{value:''},'Все категории'),...[...new Set(items.map(i=>i.category))].sort((a,b)=>a.localeCompare(b,'ru')).map(c=>el('option',{value:c},c)));search.oninput=category.onchange=()=>{limit=48;draw();};draw();}catch(e){icons.replaceChildren(el('p',{role:'alert'},e.message),button('Повторить',load));}};await load();
   colors.append(button('Сбросить оформление',()=>{update(color,'');update(icon,'');}));search.focus();
  }
  host.addEventListener('change',drawPreview);host.addEventListener('input',drawPreview);return read;
 };
 P.renderMyIcons=async function(host){
  host.replaceChildren(el('h2',{},'Мои иконки'),meta('PNG и WebP · до 1 MiB · до 1024 × 1024 px. Изображения хранятся в резервной копии планировщика.'));
  const input=el('input',{type:'file',multiple:true,accept:'image/png,image/webp','aria-label':'Загрузить иконки'});const upload=button('Загрузить выбранные',load,'pl-primary');const message=el('div',{role:'status'});const list=el('div',{class:'pl-custom-catalog'});host.append(el('div',{class:'pl-actions'},input,upload),message,list);
  async function draw(){try{const items=(await P.iconCatalog(true)).filter(i=>i.kind==='custom');list.replaceChildren(...items.map(i=>{const row=el('article',{class:'pl-custom-row'},P.appearanceIcon({icon_id:i.id}),el('strong',{},i.name));row.append(button('Переименовать',()=>edit(i,false)),button('Удалить',()=>edit(i,true)));return row;}));if(!items.length)list.append(meta('Здесь появятся ваши изображения. Встроенные иконки уже доступны в редакторах.'));}catch(e){list.replaceChildren(el('p',{role:'alert'},e.message),button('Повторить',draw));}}
  function edit(i,remove){const {dialog,close}=P.dialog(remove?'Удалить иконку из каталога?':'Переименовать иконку');const form=el('form',{'data-submit':remove?'Удалить иконку':'Переименовать иконку'});dialog.append(form);if(remove)form.append(el('p',{},'Иконка исчезнет из выбора. Использующие её задачи, проекты и версии шаблонов сохранят изображение и историю.'));const name=!remove?P.field(form,'name','Название иконки','text',i.name):null;if(name){name.required=true;name.maxLength=120;}
   P.bindForm(form,()=>P.api('/icons/'+encodeURIComponent(i.id),remove?'DELETE':'PATCH',{expected_revision:i.revision,...(name?{name:name.value}:{})}),async()=>{close(true);await draw();},async()=>{const latest=(await P.iconCatalog(true)).find(x=>x.id===i.id);if(!latest)throw new Error('Иконка уже удалена из каталога. Закройте окно и обновите список.');return {record:{...latest,title:latest.name},accept(){i=latest;}};});
  }
  async function load(){if(upload.disabled)return;const files=[...input.files];if(!files.length){message.textContent='Выберите PNG или WebP.';return;}upload.disabled=true;input.disabled=true;message.replaceChildren();let failed=false;
   for(const file of files){try{if(file.size>1048576)throw new Error('Файл больше 1 MiB');const data=await new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(String(reader.result).split(',')[1]);reader.onerror=reject;reader.readAsDataURL(file);});await P.api('/icons','POST',{name:file.name.replace(/\.[^.]+$/,''),data});message.append(el('p',{},file.name+' — добавлена'));}catch(e){failed=true;message.append(el('p',{role:'alert'},file.name+' — '+e.message));}}
   if(!failed)input.value='';input.disabled=false;upload.disabled=false;await draw();
  }await draw();
 };
})(window.Planner);

(function(P){
 'use strict';const {el,button,meta}=P;
 P.editSeriesAppearance=async function(id){
  try{
   const previousGuard=P.guardActive,previousNavigation=P.deferNavigation;
   let series=await P.api('/series/'+id+'?limit=1');
   const {dialog,close}=P.dialog('Оформление всего повторения');const form=el('form',{'data-submit':'Предпросмотр оформления'});
   dialog.append(el('p',{},P.seriesTitle(series)),meta('Все незавершённые и будущие выполнения. Завершённая история и индивидуальные переопределения сохраняются. Даты и расписание не изменятся.'),form);
   const read=P.appearanceField(form,series.project_template||series.template,series.source_type==='project');
   const preview=el('section',{class:'pl-recurrence-preview'});dialog.append(preview);let input,result;
   const send=P.commandSender('/series/'+id+'/appearance');
   const apply=button('Применить оформление',async()=>{if(!result)return;apply.disabled=true;form.dataset.busy='true';try{await send({...input,preview_token:result.preview_token});delete form.dataset.uncertain;close(true);await P.refresh();P.notify('«'+P.seriesTitle(series)+'»: оформление повторения обновлено');}catch(e){preview.append(el('p',{role:'alert'},e.message));if(e.uncertainCommand)form.dataset.uncertain='true';else {delete form.dataset.uncertain;result=null;apply.hidden=true;}}finally{delete form.dataset.busy;apply.disabled=false;}},'pl-primary');apply.hidden=true;preview.after(apply);
   form.addEventListener('input',()=>{if(!form.dataset.uncertain){result=null;apply.hidden=true;preview.replaceChildren();}});
   P.bindForm(form,async()=>{series=await P.api('/series/'+id+'?limit=1');input={...read(),expected_revision:series.revision};return P.api('/series/'+id+'/appearance-preview','POST',input);},p=>{delete form.dataset.saved;result=p;preview.replaceChildren(el('h3',{},'Было → Станет'),el('p',{},'Выполнений: '+p.rows.length+' · история защищена: '+p.rows.filter(r=>r.protected).length+' · индивидуальное оформление сохранено: '+p.rows.filter(r=>r.individual).length));
    preview.append(el('div',{class:'pl-style-comparison'},meta('Источник оформления'),P.appearanceIcon(p.before,series.source_type==='project'),meta(P.colorName(p.before.color_id)+' → '+P.colorName(p.after.color_id)),P.appearanceIcon(p.after,series.source_type==='project')));const rows=el('details',{},el('summary',{},'Изменения каждого выполнения'));preview.append(rows);
    for(const r of p.rows)rows.append(el('div',{class:'pl-preview-row'},el('span',{},r.title),P.appearanceIcon(r.before,series.source_type==='project'),meta('→'),P.appearanceIcon(r.after,series.source_type==='project'),meta(r.protected?'История сохранена':r.individual?'Индивидуальные свойства сохранены':'Оформление обновится')));
    apply.hidden=false;P.showPreview(preview);
   });const guard=P.guardForm(form,'series-appearance:'+id,null,dialog,{previewOnly:true});dialog.addEventListener('close',()=>{if(P.guardActive===guard){P.guardActive=previousGuard;P.deferNavigation=previousNavigation;}});
  }catch(e){P.notify(e.message);}
 };
})(window.Planner);
