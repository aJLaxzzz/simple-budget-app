const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
function fixture(){
 const state={revision:1,generation:'one',body:{title:'synthetic'},calls:0,wait:null};
 const context={window:{dispatchEvent(){}},location:{hash:'#/plans/inbox'},Event:class{},CustomEvent:class{},fetch:async()=>{
   state.calls++;const body=structuredClone(state.body),revision=state.revision,generation=state.generation,wait=state.wait;
   if(wait)await wait;
   return {ok:true,status:200,headers:{get:n=>({'X-Budget-Sync-Generation':generation,'X-Budget-Sync-Revision':String(revision)})[n]??null},json:async()=>body};
 }};
 for(const file of ['view-sync','api'])vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/js/'+file+'.js'),'utf8'),context);
 return {...context,state,v:context.window.budgetViews,api:context.window.budgetAPI};
}
test('view acknowledges only its oldest GET watermark, never an interleaved commit',async()=>{
 const {v,api,state}=fixture();await v.run(async()=>{await api('/api/planner/tasks');state.revision=2;await api('/api/planner/projects')});
 assert.equal(v.covers({generation:'one',revision:1}),true);assert.equal(v.covers({generation:'one',revision:2}),false);assert.equal(v.covers({generation:'two',revision:0}),false);
});
test('identical background data skips rendering; changed data reuses the one GET',async()=>{
 const {v,api,state}=fixture();let renders=0;
 const render=async()=>{const data=await api('/api/planner/tasks');data.title='mutated locally';renders++};
 await v.run(render);await v.run(render,true);assert.equal(renders,1);assert.equal(state.calls,2);
 state.body.title='new';state.revision++;await v.run(render,true);assert.equal(renders,2);assert.equal(state.calls,3);assert.equal(v.covers({generation:'one',revision:2}),true);
});
test('newer render wins over a late GET',async()=>{
 const {v,api,state}=fixture();let release,screen;
 state.wait=new Promise(r=>release=r);const old=v.run(async()=>{screen=(await api('/api/planner/tasks')).title});
 await Promise.resolve();state.wait=null;state.body.title='new';state.revision++;
 await v.run(async()=>{screen=(await api('/api/planner/tasks')).title});release();await old;assert.equal(screen,'new');
});
test('a draft started during GET prevents applying its response',async()=>{
 const {v,api,state,window}=fixture();let release,painted=false;
 state.wait=new Promise(r=>release=r);const run=v.run(async()=>{await api('/api/planner/tasks');painted=true});await Promise.resolve();
 window.budgetEditEpoch=1;window.budgetDraftDirty=true;release();await run;
 assert.equal(painted,false);assert.equal(window.budgetDraftDirty,true);assert.ok(!v.covers({generation:'one',revision:1}));
});
test('a mutation invalidates an outstanding read without suppressing the mutation',async()=>{
 const {v,api,state}=fixture();let release,painted=false;
 state.wait=new Promise(r=>release=r);const old=v.run(async()=>{await api('/api/planner/tasks');painted=true});await Promise.resolve();state.wait=null;
 await api('/api/planner/tasks',{method:'POST'});release();await old;assert.equal(painted,false);
});
test('navigation invalidates a late view and server restart requires fresh coverage',async()=>{
 const {v,api,state,location}=fixture();let release,painted=false;
 state.wait=new Promise(r=>release=r);const old=v.run(async()=>{await api('/api/planner/tasks');painted=true});await Promise.resolve();location.hash='#/plans/projects';release();await old;assert.equal(painted,false);
 state.wait=null;state.generation='two';await v.run(()=>api('/api/planner/projects'));assert.equal(v.covers({generation:'two',revision:1}),true);assert.equal(v.covers({generation:'one',revision:1}),false);
});
test('a read begun during a save is invalidated when that save commits',async()=>{
 const {v,api,state}=fixture();let releaseWrite,releaseRead,painted=false;
 state.wait=new Promise(r=>releaseWrite=r);const write=api('/api/planner/tasks',{method:'POST'});await Promise.resolve();
 state.wait=new Promise(r=>releaseRead=r);const read=v.run(async()=>{await api('/api/planner/tasks');painted=true});await Promise.resolve();
 releaseWrite();await write;releaseRead();await read;assert.equal(painted,false);
});
test('a pointer/keyboard interaction begun during a read defers reconciliation',async()=>{
 const {v,api,state,window}=fixture();let release,painted=false;
 state.wait=new Promise(r=>release=r);const run=v.run(async()=>{await api('/api/planner/tasks');painted=true},true);await Promise.resolve();window.budgetInteractionEpoch=1;release();await run;assert.equal(painted,false);
});

test('a user action is not attached to a superseded background read',async()=>{
 const {v,api,state,window}=fixture();let release,painted=false;
 state.wait=new Promise(r=>release=r);const old=v.run(async()=>{await api('/api/planner/tasks');painted=true},true);await Promise.resolve();
 window.budgetInteractionEpoch=1;state.wait=null;
 assert.equal((await api('/api/planner/tasks/selected')).title,'synthetic');
 release();await old;assert.equal(painted,false);
});
