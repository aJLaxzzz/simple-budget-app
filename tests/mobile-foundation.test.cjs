const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
// Same-document history with browser-like forward truncation. No app data.
function historyHarness(initial='#/plans/tasks'){
 const events=new Map(),entries=[{hash:initial,state:{otherFeature:'preserved'}}];let index=0;
 const emit=()=>{for(const cb of events.get('hashchange')||[])cb({type:'hashchange'});};
 const location={get hash(){return entries[index].hash;},set hash(hash){entries.splice(index+1);entries.push({hash,state:null});index++;emit();},get href(){return 'http://localhost/'+this.hash;}};
 const history={get state(){return entries[index].state;},get length(){return entries.length;},replaceState(state,unused,hash){entries[index]={hash,state};},go(delta){if(index+delta<0||index+delta>=entries.length)return;index+=delta;emit();},back(){this.go(-1);}};
 const window={location,history,addEventListener(type,callback){const all=events.get(type)||[];all.push(callback);events.set(type,all);},dispatchEvent:emit,scrollY:120,scrollX:0};
 const document={activeElement:{},addEventListener(){},getElementById(){return null;}};
 vm.runInNewContext(fs.readFileSync('static/js/planner/core.js','utf8'),{window,document,URLSearchParams,Intl,Date,Math,clearTimeout,HashChangeEvent:class{}});
 return {P:window.Planner,history,location};
}
test('foundation close goes back to the originating filtered view without another history entry',()=>{
 const {P,history,location}=historyHarness('#/plans/tasks?q=hello');P.openTask('task-1');assert.equal(history.length,2);P.closeEditor();assert.equal(location.hash,'#/plans/tasks?q=hello');assert.equal(history.length,2);history.go(1);assert.equal(location.hash,'#/plans/tasks?q=hello&task=task-1');
});
test('foundation cancelled Back restores original entry, retains Forward and does not grow history',()=>{
 const {P,history,location}=historyHarness();P.newTask();const editor=P.historyEntry();history.back();P.restoreEditorEntry(editor);assert.equal(location.hash,'#/plans/tasks?new=task');assert.equal(history.length,2);assert.equal(P.consumeHistoryRestore(),true);assert.equal(P.consumeHistoryRestore(),false);history.back();assert.equal(location.hash,'#/plans/tasks');history.go(1);assert.equal(location.hash,editor.hash);
});
test('foundation calendar canonicalization preserves entry identity and unrelated state',()=>{
 const {P,history,location}=historyHarness('#/plans/calendar');P.replaceRoute('#/plans/calendar?mode=day&date=2026-11-26');assert.equal(history.state.otherFeature,'preserved');P.openTask('calendar-task');P.replaceRoute(location.hash);P.closeEditor();assert.equal(location.hash,'#/plans/calendar?mode=day&date=2026-11-26');assert.equal(history.length,2);
});
test('foundation direct editor link closes in place instead of leaving the application',()=>{
 const {P,history,location}=historyHarness('#/plans/tasks?q=hello&new=task');P.closeEditor();assert.equal(location.hash,'#/plans/tasks?q=hello');assert.equal(history.length,1);assert.equal(history.state.otherFeature,'preserved');
});
