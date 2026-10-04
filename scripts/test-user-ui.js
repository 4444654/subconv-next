#!/usr/bin/env node
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'internal/api/static/account.js'), 'utf8');
function fixture(reply = {ok:true, authenticated:true, csrf_token:'next-csrf'}) {
 const nodes = {}, requests = [], messages = [];
 const ids = ['change-password-btn','users-btn','password-form','password-dialog','current-password','new-password','new-password-confirm','password-message','users-dialog','refresh-users-btn','users-list','users-count','reset-user-form','reset-user-dialog','reset-user-password','reset-user-confirm','reset-user-message','reset-user-title'];
 for (const id of ids) {
  const classes = new Set();
  nodes[id] = { value:'', textContent:'', innerHTML:'', disabled:false, hidden:false, open:false, events:{}, classes,
   classList:{toggle(name,enabled){if(enabled)classes.add(name);else classes.delete(name);}},
   addEventListener(name,fn){this.events[name]=fn;}, focus(){},
   querySelectorAll(){return Object.values(nodes).filter(n=>n!==this);},
   reset(){for(const key of id==='password-form'?['current-password','new-password','new-password-confirm']:['reset-user-password','reset-user-confirm'])nodes[key].value='';},
   showModal(){this.open=true;}, close(){this.open=false;this.events.close?.();},
  };
 }
 const context = vm.createContext({document:{getElementById:id=>nodes[id]}, state:{csrfToken:'old-csrf'}, TextEncoder, Date,
  escapeHtml:text=>String(text).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),
  showToast(text){messages.push(text);},
  fetchJSON:async(url,options)=>{requests.push({url,options});return typeof reply==='function'?reply(url,options):reply;},
 });
 vm.runInContext(source,context);
 vm.runInContext('initializeAccountControls()',context);
 return {nodes,requests,messages,context};
}
async function run() {
 const f = fixture();
 vm.runInContext('updateAccountControls({required:true,authenticated:true,role:"user"})',f.context);
 assert.equal(f.nodes['change-password-btn'].classes.has('hidden'),false);
 assert.equal(f.nodes['users-btn'].classes.has('hidden'),true);
 vm.runInContext('updateAccountControls({required:true,authenticated:true,role:"admin"})',f.context);
 assert.equal(f.nodes['users-btn'].classes.has('hidden'),false);
 assert.equal(f.nodes['change-password-btn'].classes.has('hidden'),true);
 vm.runInContext('updateAccountControls({required:false,authenticated:true})',f.context);
 assert.equal(f.nodes['change-password-btn'].classes.has('hidden'),true);
 f.nodes['current-password'].value='  current password!  ';
 f.nodes['new-password'].value='  new password!  ';
 f.nodes['new-password-confirm'].value='different';
 await vm.runInContext('changeOwnPassword({preventDefault(){}})',f.context);
 assert.equal(f.requests.length,0);
 assert.match(f.nodes['password-message'].textContent,/不一致/);
 f.nodes['new-password'].value=f.nodes['new-password-confirm'].value='密'.repeat(25);
 await vm.runInContext('changeOwnPassword({preventDefault(){}})',f.context);
 assert.equal(f.requests.length,0);
 f.nodes['new-password'].value=f.nodes['new-password-confirm'].value='  new password!  ';
 await vm.runInContext('changeOwnPassword({preventDefault(){}})',f.context);
 assert.equal(f.requests[0].url,'/api/auth/password');
 assert.deepEqual(JSON.parse(f.requests[0].options.body),{current_password:'  current password!  ',new_password:'  new password!  ',confirm_password:'  new password!  '});
 assert.equal(f.context.state.csrfToken,'next-csrf');
 assert.equal(f.nodes['new-password'].value,'');
 assert.match(f.messages[0],/密码已修改/);
 const bad = fixture({ok:false,error:{code:'CURRENT_PASSWORD_INVALID'}});
 bad.nodes['current-password'].value='incorrect';
 bad.nodes['new-password'].value=bad.nodes['new-password-confirm'].value='new password123';
 bad.nodes['password-dialog'].showModal();
 await vm.runInContext('changeOwnPassword({preventDefault(){}})',bad.context);
 assert.match(bad.nodes['password-message'].textContent,/当前密码不正确/);
 assert.equal(bad.nodes['password-dialog'].open,true);
 assert.equal(bad.nodes['current-password'].disabled,false);
 const user = {id:'u_alice',username:'alice',created_at:'2026-10-04T08:00:00Z',disabled:false};
 const users = fixture((url,options)=> {
  if(url==='/api/users')return {ok:true,users:[user],limit:256};
  if(options.method==='PATCH')user.disabled=true;
  return {ok:true};
 });
 await vm.runInContext('loadManagedUsers()',users.context);
 assert.match(users.nodes['users-list'].innerHTML,/alice/);
 assert.equal(users.nodes['users-count'].textContent,'1 / 256 个注册用户');
 users.context.event={target:{closest:()=>({dataset:{userId:'u_alice',userAction:'toggle'},disabled:false})}};
 await vm.runInContext('handleManagedUserAction(event)',users.context);
 const toggle=users.requests.find(x=>x.options?.method==='PATCH');
 assert.equal(toggle.url,'/api/users/u_alice');
 assert.deepEqual(JSON.parse(toggle.options.body),{disabled:true});
 assert.match(users.nodes['users-list'].innerHTML,/已停用/);
 users.context.event={target:{closest:()=>({dataset:{userId:'u_alice',userAction:'password'}})}};
 await vm.runInContext('handleManagedUserAction(event)',users.context);
 assert.equal(users.nodes['reset-user-dialog'].open,true);
 users.nodes['reset-user-password'].value=users.nodes['reset-user-confirm'].value='replacement123';
 await vm.runInContext('resetManagedUserPassword({preventDefault(){}})',users.context);
 const reset=users.requests.find(x=>x.url.endsWith('/password'));
 assert.equal(reset.url,'/api/users/u_alice/password');
 assert.deepEqual(JSON.parse(reset.options.body),{new_password:'replacement123',confirm_password:'replacement123'});
 assert.equal(users.nodes['reset-user-dialog'].open,false);
 assert.equal(users.nodes['reset-user-password'].value,'');
 const unsafe=fixture({ok:true,users:[{...user,username:'<img src=x onerror=alert(1)>'}],limit:256});
 await vm.runInContext('loadManagedUsers()',unsafe.context);
 assert.doesNotMatch(unsafe.nodes['users-list'].innerHTML,/<img/);
 assert.match(unsafe.nodes['users-list'].innerHTML,/&lt;img/);
 const offline=fixture({ok:false,error:{code:'NETWORK_ERROR'}});
 await vm.runInContext('loadManagedUsers()',offline.context);
 assert.match(offline.nodes['users-list'].textContent,/无法连接/);
 assert.equal(offline.nodes['refresh-users-btn'].disabled,false);
 process.stdout.write('User UI checks passed: role-specific controls, password validation and CSRF refresh, user listing, disable/enable payloads, password reset, secret cleanup and escaped output.\n');
}
run().catch(error=>{process.stderr.write(`${error.stack}\n`);process.exitCode=1;});
