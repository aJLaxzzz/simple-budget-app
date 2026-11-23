(function(P){
 'use strict';const {el,button}=P;const expanded=new Map();
 P.disclosure=function(title,summary=''){
   const hint=el('span',{class:'pl-disclosure-hint'},summary);
   const section=el('details',{class:'pl-disclosure',open:expanded.get(title)||false},el('summary',{},el('span',{},title),hint));section.hint=hint;section.addEventListener('toggle',()=>expanded.set(title,section.open));return section;
 };
 P.guardForm=function(form,key,record,container,options={}){
   const initial=P.values(form);
   // Restored drafts remain unsaved, rather than becoming the new baseline.
   const baseline={...initial};if(record)for(const k of Object.keys(initial))if(k in record)baseline[k]=String(record[k]??'');
   let prompting;
   const dirty=()=>form.isConnected&&!form.dataset.saved&&JSON.stringify(P.values(form))!==JSON.stringify(baseline);
   const guard=async()=>{
     if(form.dataset.busy==='true'||form.dataset.uncertain||container.querySelector('form[data-busy="true"],form[data-uncertain="true"]')){P.notify('Дождитесь ответа или повторите сохранение этой операции.');return false;}
     if(!dirty())return true;
     if(prompting)return prompting;
     prompting=new Promise(resolve=>{
       const {dialog,close}=P.dialog('Несохранённые изменения');dialog.setAttribute('aria-label','Несохранённые изменения');
       let decided=false;const finish=value=>{decided=true;close(true);prompting=null;resolve(value);};
       dialog.addEventListener('close',()=>{if(!decided){prompting=null;resolve(false);}});
       dialog.append(el('p',{},'В форме есть несохранённые изменения.'),el('div',{class:'pl-menu-items'},
         !options.previewOnly&&button('Сохранить и продолжить',()=>{if([...form.elements].some(c=>c.type==='submit'&&c.disabled)){finish(false);P.notify('Сначала просмотрите актуальную версию и разрешите конфликт в форме.');return;}decided=true;close(true);form.addEventListener('planner-saved',()=>{prompting=null;resolve(true);},{once:true});form.addEventListener('planner-failed',()=>{prompting=null;resolve(false);},{once:true});if(!form.reportValidity()){prompting=null;resolve(false);}else form.requestSubmit();},'pl-primary'),
         button('Отменить изменения',()=>{P.drafts.delete(key);for(const [k,v] of Object.entries(baseline)){const input=form.elements.namedItem(k);if(input)input.value=v;}finish(true);}),button('Продолжить редактирование',()=>finish(false))));
     });return prompting;
   };
   container.plannerGuard=guard;
   const hash=window.location.hash;
   P.guardActive=guard;
   let navigating=false;
   P.deferNavigation=function(resume){
     if(!container.isConnected||window.location.hash===hash)return false;
     if(!dirty()&&!form.dataset.busy&&!form.dataset.uncertain)return false;
     if(navigating)return true;
     navigating=true;
     // Keep the destination's history entry intact while the old form remains
     // visible. Cancelling returns to the editor without overwriting that entry.
     guard().then(ok=>{navigating=false;if(ok){P.guardActive=null;P.deferNavigation=null;resume();}else window.history.pushState(null,'',hash);});return true;
   };
   return guard;
 };
})(window.Planner);
