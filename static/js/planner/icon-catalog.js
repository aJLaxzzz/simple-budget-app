(function(P){
 'use strict';
 const normalize=value=>String(value||'').toLocaleLowerCase('ru').replace(/ё/g,'е').trim();
 P.iconSearch=(items,query='',category='')=>{const words=normalize(query).split(/\s+/).filter(Boolean);return items.filter(item=>(!category||item.category===category)&&words.every(word=>normalize([item.name,item.id,item.category,...(item.synonyms||[])].join(' ')).includes(word)));};
 P.recentIcons=()=>{try{const value=JSON.parse(localStorage.getItem('planner-recent-icons')||'[]');return Array.isArray(value)?value.filter(id=>typeof id==='string').slice(0,8):[];}catch(_){return [];}};
 P.rememberIcon=id=>{try{localStorage.setItem('planner-recent-icons',JSON.stringify([id,...P.recentIcons().filter(v=>v!==id)].slice(0,8)));}catch(_){/* optional preference */}};
 P.iconSuggestions=items=>{const ids=[...P.recentIcons(),'lucide:house','lucide:broom','lucide:scissors','lucide:coffee','lucide:shopping-cart','lucide:briefcase','lucide:heart-pulse','lucide:cat'];return [...new Set(ids)].map(id=>items.find(i=>i.id===id)).filter(Boolean).slice(0,8);};
})(window.Planner);
