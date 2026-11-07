const $=id=>document.getElementById(id);let latestPorts=[];let portNetworkContext=new Map();let trafficHistory=[];let previousCounters=null;let previousAt=0;let previousPortCounters=new Map();let portRates=new Map();
const portTrafficHistory=new Map();let recentEvents=[];let portHistory=[];let counterHistory=[];let previousPortStates=new Map();let previousErrorCounters=new Map();let previousReachable=null;let hasPortBaseline=false;
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const natural=(a,b)=>String(a??'').localeCompare(String(b??''),'en',{numeric:true,sensitivity:'base'});
const sorted=ports=>[...ports].sort((a,b)=>natural(a.name,b.name)||Number(a.index)-Number(b.index));
async function api(path,body){const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),30000);let r;try{r=await fetch(path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),cache:'no-store',signal:controller.signal});}catch(e){if(e.name==='AbortError')throw Error('Przekroczono limit czasu odpowiedzi API (30 s): '+path);throw e;}finally{clearTimeout(timer);}const d=await r.json().catch(()=>({error:'Nieprawidłowa odpowiedź API'}));if(r.status===401){location.href='/login';throw Error('Sesja wygasła — zaloguj się ponownie.');}if(!r.ok)throw Error(d.error||'Błąd API');return d;}

function activatePage(hash){
 const main=document.querySelector('.main');if(!main)return;
 const targetId=(hash||'#overview').slice(1),target=document.getElementById(targetId);
 const navLinks=[...document.querySelectorAll('.sidebar .nav')];
 navLinks.forEach(a=>a.classList.toggle('active',a.getAttribute('href')==='#'+targetId));
 const topbar=main.querySelector('.topbar');
 [...main.children].forEach(node=>{if(node===topbar||node.id==='port-dialog')return;node.hidden=true;});
 if(targetId==='overview'){
  ['overview','warning','network-dashboard'].forEach(id=>{const el=document.getElementById(id);if(el)el.hidden=false;});
  const metrics=main.querySelector('.metrics');if(metrics)metrics.hidden=false;
  return;
 }
 if(!target){activatePage('#overview');return;}
 let node=target;
 while(node&&node!==main){
  node.hidden=false;
  const parent=node.parentElement;
  if(parent&&parent!==main){
   [...parent.children].forEach(sibling=>{if(sibling!==node)sibling.hidden=true;});
   parent.hidden=false;
  }
  node=parent;
 }
}
function initPageNavigation(){
 document.querySelectorAll('.sidebar .nav').forEach(a=>a.addEventListener('click',e=>{
  const hash=a.getAttribute('href');if(!hash||!hash.startsWith('#'))return;
  e.preventDefault();history.replaceState(null,'',hash);activatePage(hash);
 }));
 window.addEventListener('hashchange',()=>activatePage(location.hash));
 activatePage(location.hash||'#overview');
}
initPageNavigation();
function initNavigationSearch(){
 const input=$('nav-search');
 if(!input)return;
 const links=[...document.querySelectorAll('.sidebar .nav')];
 const empty=document.createElement('div');
 empty.className='nav-search-empty';
 empty.textContent='Brak pasujących sekcji';
 empty.hidden=true;
 input.closest('.nav-search-wrap').insertAdjacentElement('afterend',empty);
 const filter=()=>{
  const query=input.value.trim().toLocaleLowerCase('pl');
  let visible=0;
  links.forEach(link=>{
   const match=!query||link.textContent.toLocaleLowerCase('pl').includes(query);
   link.hidden=!match;
   if(match)visible++;
  });
  empty.hidden=visible!==0;
 };
 input.addEventListener('input',filter);
 input.addEventListener('keydown',event=>{
  if(event.key==='Escape'){input.value='';filter();input.blur();return;}
  if(event.key==='Enter'){
   const first=links.find(link=>!link.hidden);
   if(first)first.click();
  }
 });
 document.addEventListener('keydown',event=>{
  const target=event.target;
  const typing=target instanceof HTMLElement&&(target.isContentEditable||['INPUT','TEXTAREA','SELECT'].includes(target.tagName));
  if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==='k'){
   event.preventDefault();input.focus();input.select();return;
  }
  if(event.key==='/'&&!typing&&!event.ctrlKey&&!event.metaKey&&!event.altKey){
   event.preventDefault();input.focus();
  }
 });
}
initNavigationSearch();

function syncPortChoices(){for(const id of ['cfg-port','edgeport-port','detail-port']){const sel=$(id),old=sel.value;sel.innerHTML=latestPorts.map(p=>'<option value="'+esc(p.name)+'">'+esc(p.name)+'</option>').join('');if([...sel.options].some(o=>o.value===old))sel.value=old;}}
function render(){const q=$('filter').value.toLowerCase(),st=$('state-filter').value;const ps=latestPorts.filter(p=>(p.name||'').toLowerCase().includes(q)&&(st==='all'||p.oper===st));$('port-count').textContent=ps.length+' / '+latestPorts.length+' portów';$('ports').innerHTML=ps.length?ps.map(p=>{const on=p.admin==='up';return '<tr><td><b>'+esc(p.name)+'</b></td><td><span class="state '+(on?'up':'down')+'">'+esc(p.admin)+'</span></td><td><span class="state '+(p.oper==='up'?'up':p.oper==='down'?'down':'unknown')+'">'+esc(p.oper)+'</span></td><td>'+esc(p.speed||'—')+'</td><td>'+fmtCount(p.inOctets)+'</td><td>'+fmtCount(p.outOctets)+'</td><td>'+fmtCount(p.inErrors)+' / '+fmtCount(p.outErrors)+'</td><td>'+(Number(p.mtu)||'—')+'</td><td><button class="port-action '+(on?'danger':'')+'" data-port="'+esc(p.name)+'" data-action="'+(on?'disable':'enable')+'">'+(on?'Wyłącz port':'Włącz port')+'</button> <button class="port-action" data-config-port="'+esc(p.name)+'">Konfiguruj</button></td></tr>'}).join(''):'<tr><td colspan="9" class="empty">Brak portów pasujących do filtra.</td></tr>';}
function fmtDuration(seconds){const n=Math.max(0,Math.floor(seconds));if(n<60)return n+' s';const d=Math.floor(n/86400),h=Math.floor(n%86400/3600),m=Math.floor(n%3600/60);return (d?d+' d ':'')+(h?h+' godz. ':'')+(m?m+' min':'')||'0 min';}
function portOutages(){const byPort=new Map();for(const e of [...portHistory].sort((a,b)=>new Date(a.timestamp)-new Date(b.timestamp))){if(!byPort.has(e.port))byPort.set(e.port,[]);byPort.get(e.port).push(e);}const result=[];for(const [port,events] of byPort){let down=null;for(const e of events){if(e.state==='down'&&!down)down=e;else if(e.state==='up'&&down){result.push({port,down:down.timestamp,up:e.timestamp,seconds:Math.max(0,(new Date(e.timestamp)-new Date(down.timestamp))/1000),active:false});down=null;}}if(down)result.push({port,down:down.timestamp,up:null,seconds:Math.max(0,(Date.now()-new Date(down.timestamp))/1000),active:true});}return result.sort((a,b)=>new Date(b.down)-new Date(a.down));}
function renderPortHistory(){const select=$('port-history-select'),rows=$('port-history-rows'),rank=$('port-failure-rank');if(!select||!rows||!rank)return;const ports=[...new Set([...latestPorts.map(p=>p.name),...portHistory.map(e=>e.port)])].filter(Boolean).sort(natural);const selected=select.value;select.innerHTML='<option value="">Wszystkie porty</option>'+ports.map(p=>'<option value="'+esc(p)+'">'+esc(p)+'</option>').join('');if(ports.includes(selected))select.value=selected;const outages=portOutages(),filtered=select.value?outages.filter(x=>x.port===select.value):outages;const total=filtered.reduce((sum,x)=>sum+x.seconds,0);$('port-history-count').textContent=String(filtered.length);$('port-history-down').textContent=fmtDuration(total);$('port-history-active').textContent=String(filtered.filter(x=>x.active).length);rows.innerHTML=filtered.length?filtered.slice(0,100).map(x=>'<tr><td><b>'+esc(x.port)+'</b></td><td>'+esc(new Date(x.down).toLocaleString())+'</td><td>'+(x.up?esc(new Date(x.up).toLocaleString()):'<span class="state down">Nadal trwa</span>')+'</td><td>'+esc(fmtDuration(x.seconds))+'</td><td><span class="state '+(x.active?'down':'up')+'">'+(x.active?'AWARIA':'PRZYWRÓCONO')+'</span></td></tr>').join(''):'<tr><td colspan="5" class="empty">Brak zarejestrowanych przejść linku DOWN/UP. Historia zaczyna się od wykrytych zmian po uruchomieniu monitoringu.</td></tr>';const counts=new Map();for(const x of outages){const v=counts.get(x.port)||{count:0,seconds:0,last:x.down};v.count++;v.seconds+=x.seconds;if(new Date(x.down)>new Date(v.last))v.last=x.down;counts.set(x.port,v);}const top=[...counts.entries()].sort((a,b)=>b[1].count-a[1].count||b[1].seconds-a[1].seconds).slice(0,10);rank.innerHTML=top.length?top.map(([p,v])=>'<tr><td><b>'+esc(p)+'</b></td><td>'+v.count+'</td><td>'+esc(fmtDuration(v.seconds))+'</td><td>'+esc(new Date(v.last).toLocaleString())+'</td></tr>').join(''):'<tr><td colspan="4" class="empty">Ranking pojawi się po pierwszej awarii portu.</td></tr>';}
function renderPortQuality(){
 const body=$('port-quality-rows'),summary=$('port-quality-summary');if(!body||!summary)return;
 const totals=new Map();
 for(const x of counterHistory){const v=totals.get(x.port)||{errors:0,discards:0,events:0};v.errors+=(Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0);v.discards+=(Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0);v.events++;totals.set(x.port,v);}
 const outages=new Map();for(const x of portOutages()){const v=outages.get(x.port)||{count:0,active:false,seconds:0};v.count++;v.active=v.active||x.active;v.seconds+=x.seconds;outages.set(x.port,v);}
 let attention=0,critical=0;
 const rows=latestPorts.map(p=>{const c=totals.get(p.name)||{errors:0,discards:0,events:0},o=outages.get(p.name)||{count:0,active:false,seconds:0};
 let score=100;score-=Math.min(55,Math.log10(1+c.errors)*18);score-=Math.min(25,Math.log10(1+c.discards)*9);score-=Math.min(30,o.count*10);if(p.oper==='down'&&p.admin==='up')score=Math.min(score,15);score=Math.max(0,Math.round(score));
 const status=p.admin==='up'&&p.oper==='down'?'LINK DOWN':score<60?'KRYTYCZNY':score<85?'UWAGA':'DOBRY';
 if(status==='KRYTYCZNY'||status==='LINK DOWN')critical++;else if(status==='UWAGA')attention++;
 return {...p,c,o,score,status};}).sort((a,b)=>a.score-b.score||b.c.errors-a.c.errors);
 summary.textContent=critical+' krytyczne · '+attention+' wymagają uwagi · '+rows.length+' portów ocenionych';
 body.innerHTML=rows.length?rows.map(p=>'<tr><td><b>'+esc(p.name)+'</b></td><td><span class="quality-score '+(p.score<60?'bad':p.score<85?'warn':'good')+'">'+p.score+'/100</span></td><td><span class="state '+(p.status==='DOBRY'?'up':p.status==='UWAGA'?'unknown':'down')+'">'+p.status+'</span></td><td>'+fmtCount(p.c.errors)+'</td><td>'+fmtCount(p.c.discards)+'</td><td>'+p.o.count+'</td><td>'+esc(p.speed||'—')+'</td></tr>').join(''):'<tr><td colspan="7" class="empty">Oczekiwanie na odczyt portów.</td></tr>';
}
function renderCounterHistory(){const rows=$('counter-history-rows'),rank=$('counter-error-rank');if(!rows||!rank)return;const records=[...counterHistory].sort((a,b)=>new Date(b.timestamp)-new Date(a.timestamp));const errors=records.reduce((s,x)=>s+(Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0),0),discards=records.reduce((s,x)=>s+(Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0),0);$('counter-history-events').textContent=records.length;$('counter-history-errors').textContent=fmtCount(errors);$('counter-history-discards').textContent=fmtCount(discards);rows.innerHTML=records.length?records.slice(0,100).map(x=>'<tr><td>'+esc(new Date(x.timestamp).toLocaleString())+'</td><td><b>'+esc(x.port)+'</b></td><td>'+fmtCount(x.errorRxDelta)+'</td><td>'+fmtCount(x.errorTxDelta)+'</td><td>'+fmtCount(x.discardRxDelta)+'</td><td>'+fmtCount(x.discardTxDelta)+'</td></tr>').join(''):'<tr><td colspan="6" class="empty">Nie wykryto jeszcze przyrostu błędów ani discard.</td></tr>';const totals=new Map();for(const x of records){const v=totals.get(x.port)||{errors:0,discards:0,events:0,last:x.timestamp};v.errors+=(Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0);v.discards+=(Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0);v.events++;if(new Date(x.timestamp)>new Date(v.last))v.last=x.timestamp;totals.set(x.port,v);}const top=[...totals.entries()].sort((a,b)=>(b[1].errors+b[1].discards)-(a[1].errors+a[1].discards)).slice(0,10);rank.innerHTML=top.length?top.map(([p,v])=>'<tr><td><b>'+esc(p)+'</b></td><td>'+fmtCount(v.errors)+'</td><td>'+fmtCount(v.discards)+'</td><td>'+v.events+'</td><td>'+esc(new Date(v.last).toLocaleString())+'</td></tr>').join(''):'<tr><td colspan="5" class="empty">Ranking pojawi się po pierwszym przyroście liczników.</td></tr>';}

function renderEvents(){
 const el=$('monitor-events');if(!el)return;
 const counts={danger:0,warning:0,info:0,success:0};
 for(const event of recentEvents){if(Object.prototype.hasOwnProperty.call(counts,event.type))counts[event.type]++;}
 for(const [id,value] of [['alert-danger-count',counts.danger],['alert-warning-count',counts.warning],['alert-info-count',counts.info],['alert-total-count',recentEvents.length]]){const node=$(id);if(node)node.textContent=String(value);}
 const filter=$('alert-filter')?.value||'all';
 const visible=recentEvents.filter(x=>filter==='all'||x.type===filter).slice(0,100);
 el.innerHTML=visible.length?visible.map(x=>'<div class="event-row is-'+esc(x.type)+'"><i></i><span>'+esc(x.message)+'</span><small>'+esc(x.at)+'</small></div>').join(''):'<div class="empty">Brak zdarzeń dla wybranego poziomu.</div>';
}
function persistMonitorRecord(record){fetch('/api/monitor/history',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(record),keepalive:true}).catch(()=>{});}
function addEvent(message,type='info',persist=true){const now=new Date();recentEvents.unshift({message,at:now.toLocaleTimeString(),timestamp:now.toISOString(),type});recentEvents=recentEvents.slice(0,12);renderEvents();if(persist)persistMonitorRecord({kind:'event',message,type});}
async function restoreMonitorHistory(){try{const d=await api('/api/monitor/history');portHistory=d.portEvents||[];counterHistory=d.counterEvents||[];renderPortHistory();renderCounterHistory();renderPortQuality();recentEvents=(d.events||[]).slice(-12).reverse().map(x=>({message:x.message,type:x.type,at:new Date(x.timestamp).toLocaleString()}));trafficHistory=(d.samples||[]).slice(-30).map(x=>({rx:Number(x.rx)||0,tx:Number(x.tx)||0}));
 for(const sample of d.portSamples||[]){const at=Date.parse(sample.timestamp)||Date.now();for(const [name,rate] of Object.entries(sample.ports||{})){const history=portTrafficHistory.get(name)||[];history.push({at,rx:Number(rate.rx)||0,tx:Number(rate.tx)||0});portTrafficHistory.set(name,history.slice(-60));portRates.set(name,{rx:Number(rate.rx)||0,tx:Number(rate.tx)||0});}}
 renderPortDetail();renderPortTraffic();renderEvents();drawTraffic();$('traffic-samples').textContent=trafficHistory.length;if(trafficHistory.length){const last=trafficHistory[trafficHistory.length-1];$('traffic-rx').textContent=formatRate(last.rx);$('traffic-tx').textContent=formatRate(last.tx);}}catch(e){console.warn('Historia monitoringu niedostępna:',e.message);}}
function recordStatusEvents(d){if(previousReachable!==null&&d.reachable!==previousReachable)addEvent(d.reachable?'Połączenie SNMP zostało przywrócone':'Utracono połączenie SNMP',d.reachable?'success':'danger');if(d.reachable){if(hasPortBaseline){for(const p of latestPorts){const oldState=previousPortStates.get(p.name);if(oldState&&oldState!==p.oper){addEvent(p.name+': link '+oldState.toUpperCase()+' → '+String(p.oper||'unknown').toUpperCase(),p.oper==='up'?'success':'danger');if((oldState==='up'||oldState==='down')&&(p.oper==='up'||p.oper==='down')){persistMonitorRecord({kind:'port',port:p.name,state:p.oper});portHistory.push({port:p.name,state:p.oper,timestamp:new Date().toISOString()});renderPortHistory();renderPortDetail();}}const old=previousErrorCounters.get(p.name);if(old){const inErrors=Number(p.inErrors||0),outErrors=Number(p.outErrors||0),inDiscards=Number(p.inDiscards||0),outDiscards=Number(p.outDiscards||0),errorRXDelta=inErrors-old.inErrors,errorTXDelta=outErrors-old.outErrors,discardRXDelta=inDiscards-old.inDiscards,discardTXDelta=outDiscards-old.outDiscards;if(errorRXDelta>0||errorTXDelta>0||discardRXDelta>0||discardTXDelta>0){const parts=[];if(errorRXDelta>0)parts.push('RX +'+errorRXDelta+' błędów');if(errorTXDelta>0)parts.push('TX +'+errorTXDelta+' błędów');if(discardRXDelta>0)parts.push('RX +'+discardRXDelta+' discard');if(discardTXDelta>0)parts.push('TX +'+discardTXDelta+' discard');addEvent(p.name+': '+parts.join(', '),'warning');const rec={kind:'counter',port:p.name,errorRxDelta:Math.max(0,errorRXDelta),errorTxDelta:Math.max(0,errorTXDelta),discardRxDelta:Math.max(0,discardRXDelta),discardTxDelta:Math.max(0,discardTXDelta),errorRxTotal:inErrors,errorTxTotal:outErrors,discardRxTotal:inDiscards,discardTxTotal:outDiscards};persistMonitorRecord(rec);counterHistory.push({...rec,timestamp:new Date().toISOString()});counterHistory=counterHistory.slice(-2000);renderCounterHistory();renderPortDetail();}}}}previousPortStates=new Map(latestPorts.map(p=>[p.name,p.oper]));previousErrorCounters=new Map(latestPorts.map(p=>[p.name,{inErrors:Number(p.inErrors||0),outErrors:Number(p.outErrors||0),inDiscards:Number(p.inDiscards||0),outDiscards:Number(p.outDiscards||0)}]));hasPortBaseline=true;}previousReachable=d.reachable;}
function renderMap(){const el=$('port-map');if(!el)return;el.innerHTML=latestPorts.map(p=>{const state=p.oper||'unknown',label=(p.name||String(p.index)).replace(/^port1\.0\./,'');return '<button class="map-port '+(state==='up'?'is-up':state==='down'?'is-down':'is-unknown')+'" data-map-port="'+esc(p.name)+'" title="'+esc(p.name)+' · '+esc(state)+'"><b>'+esc(label)+'</b><small>'+(p.speed||'—').replace(' Mb/s','M')+'</small></button>';}).join('')||'<div class="empty">Brak interfejsów SNMP.</div>';}
function fmtCount(v){const n=Number(v||0);return Number.isFinite(n)?new Intl.NumberFormat('pl-PL',{notation:n>999999?'compact':'standard',maximumFractionDigits:1}).format(n):'—';}
function portContextLines(output,port){
 const lines=String(output||'').split(/\\r?\\n/),tokens=[port.toLowerCase(),port.toLowerCase().replace(/^port/,'')].filter(Boolean);
 const matches=line=>{const value=line.toLowerCase();return tokens.some(token=>{let from=0,index;while((index=value.indexOf(token,from))>=0){const after=value[index+token.length]||'';if(!/[0-9]/.test(after))return true;from=index+1;}return false;});};
 return lines.filter(matches).slice(0,40);
}
function renderPortNetworkContext(){
 const port=$('detail-port')?.value,ctx=portNetworkContext.get(port);
 const macPattern=/\\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\\b/ig;
 for(const [id,key] of [['detail-vlan-context','vlan'],['detail-mac-context','mac'],['detail-lldp-context','lldp']]){
  const el=$(id);if(!el)continue;
  if(!port){el.textContent='Wybierz port.';continue;}
  if(!ctx){el.textContent='Kliknij „Odczytaj VLAN / MAC / LLDP”.';continue;}
  const result=ctx[key];if(result.error){el.textContent='Nie udało się odczytać danych: '+result.error;continue;}
  const output=String(result.output||'');
  if(key==='vlan'){
   const parts=output.split('\\n\\n--- show vlan all ---\\n');
   const switchport=parts[0]||'';
   const lines=switchport.split('\\n').filter(Boolean);
   const vlanLines=portContextLines(parts[1]||'',port);
   const ids=[...new Set(vlanLines.flatMap(line=>[...line.matchAll(/\\b(?:VLAN\\s*)?(\\d{1,4})\\b/gi)].map(m=>m[1])).filter(n=>Number(n)>=1&&Number(n)<=4094))];
   const mode=(switchport.match(/TRYB:\\s*([^\\n]+)/i)||[])[1]||'Brak danych';
   const native=(switchport.match(/DEFAULT \\/ NATIVE VLAN:\\s*([^\\n]+)/i)||[])[1]||'Brak danych';
   const allowed=(switchport.match(/DOZWOLONE VLAN:\\s*([^\\n]+)/i)||[])[1]||'Brak danych';
   el.textContent='Port: '+port+'\\nTryb: '+mode+'\\nNative / PVID: '+native+'\\nDozwolone VLAN: '+allowed+'\\nWpisy członkostwa: '+vlanLines.length+(ids.length?'\\nRozpoznane identyfikatory VLAN: '+ids.join(', '):'');
   continue;
  }
  const lines=portContextLines(output,port);
  if(key==='mac'){
   const macs=[...new Set(lines.flatMap(line=>line.match(macPattern)||[]).map(v=>v.toUpperCase()))];
   el.textContent=macs.length?'Urządzenia wykryte na porcie '+port+': '+macs.length+'\\n'+macs.slice(0,20).join('\\n'):'Brak rozpoznanych adresów MAC dla tego portu.';
   continue;
  }
  const names=[...new Set(lines.map(line=>line.trim()).filter(Boolean).map(line=>line.replace(/\\s+/g,' ').slice(0,100)))];
  el.textContent=names.length?'Sąsiedzi LLDP przypisani do portu '+port+': '+names.length+'\\n'+names.slice(0,8).join('\\n'):'Nie wykryto sąsiada LLDP na tym porcie.';
 }
}
async function loadPortNetworkContext(){
 const port=$('detail-port')?.value;if(!port){alert('Wybierz port.');return;}
 const button=$('load-port-network');button.disabled=true;button.textContent='Odczyt…';
 for(const id of ['detail-vlan-context','detail-mac-context','detail-lldp-context'])$(id).textContent='Pobieranie danych ze switcha…';
 try{
  const [switchport,vlans,mac,lldp]=await Promise.all([
   api('/api/port/switchport?port='+encodeURIComponent(port)),
   api('/api/command',{command:'show vlan all'}),
   api('/api/command',{command:'show mac address-table'}),
   api('/api/command',{command:'show lldp neighbors'})
  ]);
  const vlanLines=[
   'PORT: '+switchport.port,
   'TRYB: '+(switchport.mode||'nieznany'),
   'DEFAULT / NATIVE VLAN: '+(switchport.defaultVlan||'brak danych'),
   'DOZWOLONE VLAN: '+((switchport.configuredVlans||[]).join(', ')||'brak danych')
  ];
  const ctx={vlan:{output:vlanLines.join('\\n')+'\\n\\n--- show vlan all ---\\n'+(vlans.output||'')},mac:{output:mac.output||''},lldp:{output:lldp.output||''}};
  portNetworkContext.set(port,ctx);renderPortNetworkContext();
 }catch(e){
  portNetworkContext.set(port,{vlan:{error:e.message},mac:{error:e.message},lldp:{error:e.message}});
  renderPortNetworkContext();
 }finally{button.disabled=false;button.textContent='Odczytaj VLAN / MAC / LLDP';}
}
function renderPortDetail(){
 const name=$('detail-port')?.value,p=latestPorts.find(x=>x.name===name);
 if(!p){if($('detail-summary'))$('detail-summary').textContent='Wybierz port do diagnostyki.';return;}
 for(const [id,val] of Object.entries({'detail-rx':p.inOctets,'detail-tx':p.outOctets,'detail-rxe':p.inErrors,'detail-txe':p.outErrors,'detail-rxd':p.inDiscards,'detail-txd':p.outDiscards,'detail-rxp':p.inUcastPkts,'detail-txp':p.outUcastPkts}))$(id).textContent=fmtCount(val);
 const r=portRates.get(name),cap=portCapacity(p),pct=r&&cap?Math.min(100,100*(r.rx+r.tx)/cap):null;
 $('detail-summary').textContent=name+' · admin '+(p.admin||'—')+' · link '+(p.oper||'—')+' · '+(p.speed||'prędkość nieznana');
 $('detail-live-rx').textContent=r?formatRate(r.rx):'Oczekiwanie na próbki';
 $('detail-live-tx').textContent=r?formatRate(r.tx):'Oczekiwanie na próbki';
 $('detail-utilization').textContent=pct===null?'—':pct.toFixed(1)+'% (RX + TX / prędkość linku)';
 setMeterPercent($('detail-util-meter'),pct||0);
 const errors=counterHistory.filter(x=>x.port===name).slice(-8).reverse();
 $('detail-errors').innerHTML=errors.length?errors.map(x=>'<tr><td>'+esc(new Date(x.timestamp).toLocaleString())+'</td><td>'+fmtCount(x.errorRxDelta)+' / '+fmtCount(x.errorTxDelta)+'</td><td>'+fmtCount(x.discardRxDelta)+' / '+fmtCount(x.discardTxDelta)+'</td></tr>').join(''):'<tr><td colspan="3" class="empty">Brak zarejestrowanych przyrostów błędów.</td></tr>';
 const changes=portHistory.filter(x=>x.port===name).slice(-8).reverse();
 $('detail-link-events').innerHTML=changes.length?changes.map(x=>'<tr><td>'+esc(new Date(x.timestamp).toLocaleString())+'</td><td><span class="state '+(x.state==='up'?'up':'down')+'">'+esc(x.state)+'</span></td></tr>').join(''):'<tr><td colspan="2" class="empty">Brak zarejestrowanych zmian linku.</td></tr>';
 drawPortDetailTraffic();
}
function drawPortDetailTraffic(){
 const canvas=$('detail-traffic-chart');if(!canvas)return;
 const ctx=canvas.getContext('2d'),w=Math.max(250,canvas.clientWidth||600),h=180,dpr=window.devicePixelRatio||1;
 canvas.width=Math.round(w*dpr);canvas.height=Math.round(h*dpr);ctx.setTransform(dpr,0,0,dpr,0,0);ctx.clearRect(0,0,w,h);
 const samples=portTrafficHistory.get($('detail-port')?.value)||[],max=Math.max(1,...samples.flatMap(x=>[x.rx,x.tx]))*1.15;
 const left=56,right=12,top=12,bottom=22,pw=w-left-right,ph=h-top-bottom;
 ctx.font='11px system-ui';ctx.lineWidth=1;ctx.textAlign='left';
 for(let i=0;i<4;i++){const y=top+ph*i/3;ctx.strokeStyle='#26384b';ctx.beginPath();ctx.moveTo(left,y);ctx.lineTo(w-right,y);ctx.stroke();ctx.fillStyle='#8294a9';ctx.fillText(formatRate(max*(1-i/3)),2,y+4);}
 for(const [key,color] of [['rx','#398cf0'],['tx','#9a65e8']]){if(samples.length<2)continue;ctx.beginPath();samples.forEach((x,i)=>{const px=left+pw*i/(samples.length-1),py=top+ph-(x[key]/max)*ph;if(i===0)ctx.moveTo(px,py);else ctx.lineTo(px,py);});ctx.strokeStyle=color;ctx.lineWidth=2.3;ctx.stroke();}
 ctx.fillStyle='#8294a9';ctx.fillText(samples.length>1?new Date(samples[0].at).toLocaleTimeString():'Oczekiwanie na drugą próbkę',left,h-4);
 if(samples.length>1){ctx.textAlign='right';ctx.fillText(new Date(samples[samples.length-1].at).toLocaleTimeString(),w-right,h-4);}
}
function openPortDiagnostics(name){const sel=$('detail-port');if(!sel||![...sel.options].some(x=>x.value===name))return;sel.value=name;history.replaceState(null,'','#monitoring');activatePage('#monitoring');renderPortDetail();$('detail-summary')?.scrollIntoView({behavior:'smooth',block:'center'});}

function renderTopPorts(){const el=$('top-ports');if(!el)return;const rows=[...portRates.entries()].map(([name,r])=>({name,...r})).sort((a,b)=>(b.rx+b.tx)-(a.rx+a.tx)).slice(0,10);el.innerHTML=rows.length?rows.map(x=>'<tr><td><button class="port-diagnostic-link" data-diagnostic-port="'+esc(x.name)+'">'+esc(x.name)+'</button></td><td>'+esc(latestPorts.find(p=>p.name===x.name)?.oper||'—')+'</td><td>'+formatRate(x.rx)+'</td><td>'+formatRate(x.tx)+'</td><td><b>'+formatRate(x.rx+x.tx)+'</b></td></tr>').join(''):'<tr><td colspan="5" class="empty">Zbieranie próbek — ranking pojawi się po drugim odczycie.</td></tr>';}
function drawTraffic(){const canvas=$('traffic-chart');if(!canvas)return;const ctx=canvas.getContext('2d'),w=canvas.clientWidth||600,h=145,dpr=window.devicePixelRatio||1;canvas.width=w*dpr;canvas.height=h*dpr;ctx.scale(dpr,dpr);ctx.clearRect(0,0,w,h);const pad={l:42,r:8,t:10,b:22},cw=w-pad.l-pad.r,ch=h-pad.t-pad.b,max=Math.max(1,...trafficHistory.flatMap(x=>[x.rx,x.tx]))*1.18;ctx.font='10px system-ui';ctx.strokeStyle='#26384b';ctx.fillStyle='#8294a9';ctx.lineWidth=1;for(let i=0;i<4;i++){const y=pad.t+ch*i/3;ctx.beginPath();ctx.moveTo(pad.l,y);ctx.lineTo(w-pad.r,y);ctx.stroke();ctx.fillText((max*(1-i/3)/1000000).toFixed(1)+'M',2,y+3);}const plot=(key,color)=>{if(trafficHistory.length<2)return;ctx.beginPath();trafficHistory.forEach((p,i)=>{const x=pad.l+cw*i/(trafficHistory.length-1),y=pad.t+ch-(p[key]/max)*ch;if(i===0)ctx.moveTo(x,y);else ctx.lineTo(x,y);});ctx.strokeStyle=color;ctx.lineWidth=2.2;ctx.stroke();};plot('rx','#398cf0');plot('tx','#9a65e8');}
function portCapacity(p){
 const s=String(p.speed||'').toLowerCase().replace(/\s/g,'');
 if(/\b10g|10gb|10000m/.test(s))return 10e9;
 if(/\b2\.5g|2500m/.test(s))return 2.5e9;
 if(/\b5g|5000m/.test(s))return 5e9;
 if(/\b1g|1000m|gigabit/.test(s))return 1e9;
 if(/\b100m|100mb/.test(s))return 100e6;
 if(/\b10m|10mb/.test(s))return 10e6;
 const n=Number(s.replace(/[^0-9.]/g,''));if(!n)return 0;
 return s.includes('g')?n*1e9:s.includes('m')?n*1e6:n;
}
function renderPortTraffic(){
 const body=$('port-traffic-rows');if(!body)return;
 const rows=latestPorts.map(p=>{const r=portRates.get(p.name)||{rx:0,tx:0},cap=portCapacity(p),used=cap?Math.min(100,(r.rx+r.tx)/cap*100):null;
 return {...p,r,cap,used,h:portTrafficHistory.get(p.name)||[]};}).sort((a,b)=>(b.r.rx+b.r.tx)-(a.r.rx+a.r.tx));
 body.innerHTML=rows.length?rows.map(p=>'<tr><td><button class="port-diagnostic-link" data-diagnostic-port="'+esc(p.name)+'">'+esc(p.name)+'</button></td><td>'+esc(p.oper||'—')+'</td><td>'+formatRate(p.r.rx)+'</td><td>'+formatRate(p.r.tx)+'</td><td><b>'+formatRate(p.r.rx+p.r.tx)+'</b></td><td>'+(p.used===null?'—':p.used.toFixed(1)+'%')+'</td><td><div class="traffic-meter"><i class="meter-fill meter-pct-'+meterBucket(p.used===null?0:p.used)+'"></i></div></td></tr>').join(''):'<tr><td colspan="7" class="empty">Brak danych o portach.</td></tr>';
}
function sampleTraffic(){const now=Date.now(),sec=previousAt?(now-previousAt)/1000:0;let rxRate=0,txRate=0;const portSnapshot={};if(sec>0){for(const p of latestPorts){const prev=previousPortCounters.get(p.name);if(!prev)continue;const rx=Number(p.inOctets||0),tx=Number(p.outOctets||0),dr=rx-prev.rx,dt=tx-prev.tx;if(dr>=0&&dt>=0){const r={rx:dr*8/sec,tx:dt*8/sec};portRates.set(p.name,r);portSnapshot[p.name]=r;const history=portTrafficHistory.get(p.name)||[];history.push({at:now,rx:r.rx,tx:r.tx});portTrafficHistory.set(p.name,history.slice(-60));rxRate+=r.rx;txRate+=r.tx;}}if(portRates.size){trafficHistory.push({rx:rxRate,tx:txRate});trafficHistory=trafficHistory.slice(-30);persistMonitorRecord({kind:'sample',rx:rxRate,tx:txRate});if(Object.keys(portSnapshot).length)persistMonitorRecord({kind:'port_sample',ports:portSnapshot});$('traffic-rx').textContent=formatRate(rxRate);$('traffic-tx').textContent=formatRate(txRate);$('traffic-samples').textContent=trafficHistory.length;drawTraffic();renderTopPorts();renderPortTraffic();}}previousPortCounters=new Map(latestPorts.map(p=>[p.name,{rx:Number(p.inOctets||0),tx:Number(p.outOctets||0)}]));previousAt=now;renderPortDetail();renderPortTraffic();}
function meterBucket(value){return Math.max(0,Math.min(100,Math.round(Number(value)||0)));}
function setMeterPercent(element,value){element.classList.remove(...Array.from(element.classList).filter(name=>name.startsWith('meter-pct-')));element.classList.add('meter-pct-'+meterBucket(value));}
function formatRate(v){if(!Number.isFinite(v))return '—';if(v>=1e9)return (v/1e9).toFixed(2)+' Gb/s';if(v>=1e6)return (v/1e6).toFixed(1)+' Mb/s';if(v>=1e3)return (v/1e3).toFixed(0)+' kb/s';return Math.round(v)+' b/s';}
let statusLoading=false;async function load(){if(statusLoading)return;statusLoading=true;try{const d=await api('/api/status');latestPorts=sorted(d.ports||[]);recordStatusEvents(d);const up=latestPorts.filter(p=>p.oper==='up').length;$('host').textContent=d.host||'—';$('snmp').textContent=(d.snmpVersion==='3'?'SNMPv3':'SNMPv2c')+(d.reachable?' ONLINE':' OFFLINE');$('connection').classList.toggle('offline',!d.reachable);$('connection').innerHTML='<i></i> '+(d.reachable?'Połączono':'Brak połączenia');$('device-state').textContent=d.reachable?'ONLINE':'OFFLINE';$('device-state').classList.toggle('device-state-online',d.reachable);$('device-state').classList.toggle('device-state-offline',!d.reachable);$('count').textContent=latestPorts.length;$('up').textContent=up;$('updated').textContent=new Date().toLocaleTimeString();$('warning').textContent=d.warning||'';$('warning').classList.toggle('hidden',!d.warning);$('health-snmp').textContent=d.reachable?'Połączono':'Brak połączenia';$('health-snmp').className=d.reachable?'good':'bad';$('health-interfaces').textContent=latestPorts.length;$('health-up').textContent=up+' / '+latestPorts.length;$('health-time').textContent=new Date().toLocaleTimeString();$('map-online').textContent=(d.snmpVersion==='3'?'SNMPv3':'SNMPv2c')+(d.reachable?' ONLINE':' OFFLINE');syncPortChoices();render();renderMap();renderPortHistory();renderPortQuality();sampleTraffic();}catch(e){$('warning').textContent='Nie udało się pobrać danych switcha: '+e.message;$('warning').classList.remove('hidden');$('connection').classList.add('offline');$('connection').innerHTML='<i></i> Błąd odczytu';$('device-state').textContent='BRAK DANYCH';$('device-state').classList.remove('device-state-online');$('device-state').classList.add('device-state-offline');$('health-snmp').textContent='Błąd odczytu';$('health-snmp').className='bad';$('map-online').textContent='ODCZYT NIEUDANY';}finally{statusLoading=false;}}
$('bulk-apply').onclick=async()=>{
 const numbers=[...new Set($('bulk-ports').value.split(',').map(x=>x.trim()).filter(Boolean))];
 if(!numbers.length){alert('Wpisz numery portów, np. 1,2,3.');return;}
 if(numbers.some(n=>!/^\d+$/.test(n)||Number(n)<1||Number(n)>52)){alert('Porty muszą być numerami od 1 do 52.');return;}
 const payload={ports:numbers.map(n=>'port1.0.'+n),mode:$('bulk-mode').value,accessVlan:Number($('bulk-access-vlan').value),allowed:$('bulk-allowed').value,native:Number($('bulk-native').value),speed:$('bulk-speed').value,duplex:$('bulk-duplex').value,description:$('bulk-description').value};
 const out=$('bulk-output'),button=$('bulk-apply');button.disabled=true;out.textContent='Walidacja ustawień…';
 try{
  const preview=await api('/api/port/preview',{port:payload.ports[0],mode:payload.mode,accessVlan:payload.accessVlan,allowed:payload.allowed,native:payload.native,speed:payload.speed,duplex:payload.duplex,description:payload.description});
  const summary='PORTY: '+payload.ports.join(', ')+'\n\\nUSTAWIENIA WZORCOWE:\\n'+preview.commands.slice(1,-1).join('\n')+'\n\\nPrzed zmianą zostanie zapisana kopia running-config. Zastosować profil?';
  out.textContent=summary;if(!confirm(summary))return;
  const result=await api('/api/ports/settings/bulk',payload);
  out.textContent=result.output||'Konfiguracja przekazana do switcha.';
  addEvent('Zastosowano profil portów: '+payload.ports.join(', '));
  alert('Polecenia wysłane. Zweryfikuj stan portów i VLAN.');
  await load();
 }catch(e){out.textContent='BŁĄD: '+e.message;alert(e.message);}
 finally{button.disabled=false;}
};
$('bulk-admin-apply').onclick=async()=>{
 const numbers=[...new Set($('bulk-admin-ports').value.split(',').map(x=>x.trim()).filter(Boolean))];
 const action=$('bulk-admin-action').value,out=$('bulk-admin-output'),button=$('bulk-admin-apply');
 if(!numbers.length){alert('Wpisz numery portów, np. 5,6,7.');return;}
 if(numbers.some(n=>!/^[0-9]+$/.test(n)||Number(n)<1||Number(n)>52)){alert('Porty muszą być numerami od 1 do 52.');return;}
 const ports=numbers.map(n=>'port1.0.'+n);
 const warning=action==='disable'?'\n\nUWAGA: wyłączenie może odciąć urządzenia lub uplink. Serwer musi mieć ALLOW_PORT_SHUTDOWN=true.':'';
 const summary=(action==='enable'?'WŁĄCZYĆ':'WYŁĄCZYĆ')+' porty: '+ports.join(', ')+warning+'\n\nPrzed zmianą zostanie wykonana kopia konfiguracji. Kontynuować?';
 if(!confirm(summary))return;
 button.disabled=true;out.textContent='Sprawdzanie zabezpieczeń i wykonywanie operacji…';
 try{
  const result=await api('/api/ports/action/bulk',{ports,action});
  out.textContent=result.output||'Polecenia przekazane do switcha.';
  addEvent((action==='enable'?'Włączono':'Wyłączono')+' porty: '+ports.join(', '),action==='enable'?'success':'warning');
  await load();
 }catch(e){out.textContent='BŁĄD: '+e.message;alert(e.message);}
 finally{button.disabled=false;}
};
$('bulk-edge-apply').onclick=async()=>{
 const numbers=[...new Set($('bulk-edge-ports').value.split(',').map(x=>x.trim()).filter(Boolean))];
 const enabled=$('bulk-edge-enabled').value==='true',out=$('bulk-edge-output'),button=$('bulk-edge-apply');
 if(!numbers.length){alert('Wpisz numery portów, np. 1,2,3.');return;}
 if(numbers.some(n=>!/^[0-9]+$/.test(n)||Number(n)<1||Number(n)>52)){alert('Porty muszą być numerami od 1 do 52.');return;}
 const ports=numbers.map(n=>'port1.0.'+n);
 const summary=(enabled?'WŁĄCZYĆ':'WYŁĄCZYĆ')+' EdgePort na: '+ports.join(', ')+'.\n\nNie obejmuj uplinków, LACP ani połączeń switch-switch. Przed zmianą zostanie wykonana kopia konfiguracji. Kontynuować?';
 if(!confirm(summary))return;
 button.disabled=true;out.textContent='Sprawdzanie zabezpieczeń i wykonywanie…';
 try{
  const result=await api('/api/ports/edgeport/bulk',{ports,enabled});
  out.textContent=result.output||'Polecenia przekazane do switcha.';
  addEvent((enabled?'Włączono':'Wyłączono')+' EdgePort: '+ports.join(', '),'warning');
  await load();
 }catch(e){out.textContent='BŁĄD: '+e.message;alert(e.message);}
 finally{button.disabled=false;}
};
$('lacp-create').onclick=async()=>{const group=Number($('lacp-group').value),ports=$('lacp-ports').value,mode=$('lacp-mode').value;if(!confirm('Utworzyć LACP group '+group+' na portach '+ports+'? Porty powinny być wcześniej skonfigurowane identycznie po obu stronach łącza.'))return;try{const d=await api('/api/lacp',{group,ports,mode,delete:false});$('lacp-output').textContent=d.output||'Polecenie przekazane';}catch(e){alert(e.message);}};
$('lacp-delete').onclick=async()=>{const group=Number($('lacp-group').value),ports=$('lacp-ports').value;if(!confirm('Usunąć konfigurację channel-group z portów '+ports+'?'))return;try{const d=await api('/api/lacp',{group,ports,mode:$('lacp-mode').value,delete:true});$('lacp-output').textContent=d.output||'Polecenie przekazane';}catch(e){alert(e.message);}};
function parseLACPDetail(raw){
 const groups=[];let current=null;
 for(const line of String(raw||'').split(/\r?\n/)){
  let m=line.match(/^\s*Aggregator\s+(po\d+)\b.*$/i);
  if(m){current={name:m[1],partner:'—',receive:'—',transmit:'—',ready:'—',members:[]};groups.push(current);continue;}
  if(!current)continue;
  m=line.match(/^\s*Partner LAG:\s*(.+)$/i);if(m)current.partner=m[1].trim();
  m=line.match(/^\s*Receive link count:\s*(\d+)\s*-\s*Transmit link count:\s*(\d+)/i);if(m){current.receive=m[1];current.transmit=m[2];}
  m=line.match(/^\s*Individual:\s*(\d+)\s*-\s*Ready:\s*(\d+)/i);if(m)current.ready=m[2]==='1'?'Gotowy':'Niegotowy';
  m=line.match(/^\s*Link:\s*(port[0-9.]+).*?(synchronized|not synchronized)?\s*$/i);
  if(m)current.members.push({port:m[1],state:(m[2]||'stan nierozpoznany').toLowerCase()});
 }
 return groups;
}
function renderLACPDetail(raw){
 const host=$('lacp-diagnostics'),groups=parseLACPDetail(raw);
 if(!groups.length){host.innerHTML='<div class="empty">Switch zwrócił dane, ale ich format nie pozwala wiarygodnie odczytać agregatorów. Nie pokazujemy surowego tekstu.</div>';return;}
 host.innerHTML=groups.map(g=>{
  const synced=g.members.filter(x=>x.state==='synchronized').length,unsynced=g.members.length-synced;
  const rows=g.members.map(m=>'<tr><td><b>'+esc(m.port)+'</b></td><td><span class="state '+(m.state==='synchronized'?'up':'unknown')+'">'+esc(m.state)+'</span></td></tr>').join('');
  return '<section class="lacp-group-card"><div class="lacp-group-head"><h3>'+esc(g.name)+'</h3><span class="state '+(g.ready==='Gotowy'?'up':'unknown')+'">'+esc(g.ready)+'</span></div><div class="lacp-stats"><div><small>Partner LAG</small><b>'+esc(g.partner)+'</b></div><div><small>RX link count</small><b>'+esc(g.receive)+'</b></div><div><small>TX link count</small><b>'+esc(g.transmit)+'</b></div><div><small>Porty zsynchronizowane</small><b>'+synced+' / '+g.members.length+'</b></div></div><div class="table-wrap"><table><thead><tr><th>PORT CZŁONKOWSKI</th><th>STAN LACP</th></tr></thead><tbody>'+(rows||'<tr><td colspan="2" class="empty">Brak rozpoznanych portów.</td></tr>')+'</tbody></table></div>'+(unsynced?'<p class="note">Co najmniej jeden port nie ma jawnego statusu „synchronized” — sprawdź konfigurację obu końców łącza.</p>':'')+'</section>';
 }).join('');
}
$('show-lacp').onclick=async()=>{
 const status=$('lacp-status');
 $('lacp-diagnostics').innerHTML='<div class="empty">Odczytuję stan agregacji…</div>';
 status.textContent='Odczyt danych z przełącznika…';
 const commands=['show etherchannel detail','show etherchannel summary','show etherchannel'];
 let selected=null;
 for(const command of commands){
  try{
   const d=await api('/api/command',{command});
   const output=String(d.output||'').trim();
   if(output&&!/(invalid input|unknown command|unrecognized command|incomplete command|ambiguous command|polecenie niedozwolone)/i.test(output)){
    selected={command,output};break;
   }
  }catch(e){}
 }
 $('lacp-output').textContent='';
 if(selected){
  status.textContent='Odczyt zakończony.';
  renderLACPDetail(selected.output);
 }else{
  status.textContent='Switch nie zwrócił rozpoznawalnego statusu agregacji.';
  $('lacp-diagnostics').innerHTML='<div class="empty">Brak danych LACP do przedstawienia. Sprawdź obsługę poleceń przez firmware i konfigurację agregacji.</div>';
 }
};
$('apply-stp').onclick=async()=>{const mode=$('stp-mode').value,priority=Number($('stp-priority').value);if(!confirm('Zmienić globalny tryb STP na '+mode.toUpperCase()+' i priorytet bridge na '+priority+'?'))return;try{const d=await api('/api/stp',{mode,priority});$('stp-output').textContent=d.output||'Polecenie przekazane';}catch(e){alert(e.message);}};
$('show-stp').onclick=async()=>{try{const d=await api('/api/command',{command:'show spanning-tree'});$('stp-output').textContent=d.output||'Brak danych';}catch(e){$('stp-output').textContent=e.message;}};
$('edgeport-toggle').onchange=async e=>{const port=$('edgeport-port').value,enabled=e.target.checked;if(!port){alert('Wybierz port edge.');e.target.checked=false;return;}if(!confirm((enabled?'Włączyć':'Wyłączyć')+' edgeport na '+port+'? Włączaj wyłącznie na portach końcowych.')){e.target.checked=!enabled;return;}try{const d=await api('/api/edgeport',{port,enabled});$('stp-output').textContent=d.output||'Polecenie przekazane';}catch(x){e.target.checked=!enabled;alert(x.message);}};

function openPortDialog(port){
 const dialog=$('port-dialog');if(!dialog)return;
 $('cfg-port').value=port||$('cfg-port').value;
 $('cfg-description').value='';
 $('cfg-mode').value='access';$('cfg-access-vlan').disabled=false;$('cfg-allowed').disabled=true;$('cfg-native').disabled=true;
 const current=latestPorts.find(p=>p.name===$('cfg-port').value);
 if(current&&current.description)$('cfg-description').value=current.description;
 if(typeof dialog.showModal==='function')dialog.showModal();else dialog.setAttribute('open','');
}
$('close-port-dialog').onclick=()=>{const d=$('port-dialog');if(d.close)d.close();else d.removeAttribute('open');};
$('port-dialog').addEventListener('click',e=>{if(e.target===$('port-dialog'))$('port-dialog').close();});
$('cfg-mode').onchange=()=>{const trunk=$('cfg-mode').value==='trunk';$('cfg-access-vlan').disabled=trunk;$('cfg-allowed').disabled=!trunk;$('cfg-native').disabled=!trunk;};
$('ports').addEventListener('click',e=>{const b=e.target.closest('[data-config-port]');if(!b)return;openPortDialog(b.dataset.configPort);});
$('port-map').addEventListener('click',e=>{const b=e.target.closest('[data-map-port]');if(!b)return;openPortDialog(b.dataset.mapPort);});
$('dashboard-ports').onclick=()=>{history.replaceState(null,'','#ports-section');activatePage('#ports-section');};
$('dashboard-vlans').onclick=()=>$('show-vlans').click();
window.addEventListener('resize',drawTraffic);
$('detail-port').onchange=renderPortDetail;$('port-history-select').onchange=renderPortHistory;
function metricValue(raw,patterns,suffix=''){
 const text=String(raw||'');
 for(const pattern of patterns){const m=text.match(pattern);if(m)return m[1]+suffix;}
 return 'Brak danych';
}
function systemMetric(raw,kind){
 const t=String(raw||'');
 if(kind==='cpu')return metricValue(t,[/(?:CPU|processor)(?:\s+utilization|\s+usage|\s+load)?[^0-9%]{0,30}(\d+(?:\.\d+)?)\s*%/i,/(\d+(?:\.\d+)?)\s*%\s*(?:CPU|utili[sz]ation)/i],'%');
 if(kind==='memory')return metricValue(t,[/(?:memory|mem(?:ory)?)[^\n]{0,70}?(\d+(?:\.\d+)?)\s*%/i,/(\d+(?:\.\d+)?)\s*%\s*(?:used|utili[sz]ed)/i],'%');
 if(kind==='temperature')return metricValue(t,[/(?:temperature|temp)[^0-9\n]{0,30}(-?\d+(?:\.\d+)?)\s*(?:°\s*)?C\b/i,/(-?\d+(?:\.\d+)?)\s*°\s*C\b/i],' °C');
 if(kind==='fans'){const matches=[...t.matchAll(/(?:fan|ventilator)[^\n]{0,50}?(\d{2,5})\s*(?:RPM|r\/min)/gi)];return matches.length?matches.map(m=>m[1]+' RPM').join(' · '):metricValue(t,[/(?:fan|ventilator)[^\n]{0,50}\b(OK|正常|normal|failed|fault)\b/i]);}
 if(kind==='power'){const lines=t.split(/\r?\n/).filter(x=>/(?:power supply|psu|zasilacz)/i.test(x));return lines.length?lines.map(x=>/\b(ok|normal|present|active)\b/i.test(x)?'Sprawny':/\b(fail|fault|absent|not present)\b/i.test(x)?'Błąd / brak':'Status nierozpoznany').join(' · '):'Brak danych';}
 if(kind==='boot')return metricValue(t,[/(?:uptime|up time)\s*[:=]?\s*([^\n]+)/i,/(?:system up for)\s+([^\n]+)/i,/(?:last reboot|boot time)\s*[:=]?\s*([^\n]+)/i]);
 return 'Brak danych';
}
async function refreshSystem(){const button=$('refresh-system');button.disabled=true;button.textContent='Odczytuję…';try{
 const d=await api('/api/system');
 const version=String(d.version||'').split(/\r?\n/).find(x=>/(?:version|alliedware|firmware|release)/i.test(x));
 $('sys-version').textContent=version?version.trim():'Brak danych';
 $('sys-cpu').textContent=systemMetric(d.cpu,'cpu');
 $('sys-memory').textContent=systemMetric(d.memory,'memory');
 $('sys-temperature').textContent=systemMetric(d.environment,'temperature');
 $('sys-fans').textContent=systemMetric(d.environment,'fans');
 $('sys-power').textContent=systemMetric(d.environment,'power');
 $('sys-boot').textContent=systemMetric(d.boot,'boot');
 $('system-health').dataset.loaded='true';
 }catch(e){for(const id of ['sys-version','sys-cpu','sys-memory','sys-temperature','sys-fans','sys-power','sys-boot'])$(id).textContent='Błąd odczytu';$('system-health').dataset.loaded='false';}
 finally{button.disabled=false;button.textContent='Odśwież parametry';}}
$('refresh-system').onclick=refreshSystem;
$('alert-filter')?.addEventListener('change',renderEvents);
$('alert-refresh')?.addEventListener('click',async()=>{const b=$('alert-refresh');b.disabled=true;try{await restoreMonitorHistory();}finally{b.disabled=false;}});

const vlanSelectedPorts=new Set();
function updateVLANSelection(){const count=$('vlan-select-count');if(count)count.textContent='Zaznaczono: '+vlanSelectedPorts.size;const use=$('vlan-use-selected');if(use)use.disabled=vlanSelectedPorts.size===0;const apply=$('vlan-apply-membership');if(apply)apply.disabled=vlanSelectedPorts.size===0;}
async function inspectVLAN(){const id=Number($('vlan-inspect-id').value);if(!Number.isInteger(id)||id<1||id>4094){alert('VLAN ID musi być w zakresie 1–4094.');return;}vlanSelectedPorts.clear();updateVLANSelection();const box=$('vlan-inspect-raw'),map=$('vlan-port-map');box.textContent='Odczyt VLAN '+id+'…';map.innerHTML='';try{const d=await api('/api/command',{command:'show vlan '+id});const raw=d.output||'';box.textContent=raw||'Brak odpowiedzi CLI';const members=new Map();for(const m of raw.matchAll(/(port\d+\.\d+\.\d+)\s*\(([ut])\)/gi))members.set(m[1],m[2].toLowerCase());const ports=latestPorts.map(p=>{const mode=members.get(p.name)||'none';return '<div class="vlan-port-cell '+(mode==='u'?'is-untagged':mode==='t'?'is-tagged':'is-none')+'"><b>'+esc(p.name.replace(/^port1\.0\./,''))+'</b><small>'+(mode==='u'?'UNTAGGED':mode==='t'?'TAGGED':'—')+'</small><label class="vlan-port-select"><input type="checkbox" data-vlan-select="'+esc(p.name)+'" '+(vlanSelectedPorts.has(p.name)?'checked':'')+'> Zaznacz</label><button type="button" class="vlan-port-config" data-vlan-port="'+esc(p.name)+'" aria-label="Konfiguruj port '+esc(p.name)+'">Konfiguruj</button></div>';});map.innerHTML=ports.join('')||'<p class="empty">Najpierw odczytaj porty SNMP.</p>';if(members.size===0)map.insertAdjacentHTML('beforeend','<p class="note">Nie rozpoznano członków portów w odpowiedzi CLI. Sprawdź surowy wynik powyżej.</p>');}catch(e){box.textContent=e.message;map.innerHTML='<p class="empty">Nie udało się pobrać członkostwa VLAN.</p>';}}
$('inspect-vlan').onclick=inspectVLAN;
$('vlan-port-map').addEventListener('click',e=>{const button=e.target.closest('[data-vlan-port]');if(!button)return;openPortDialog(button.dataset.vlanPort);});
$('vlan-port-map').addEventListener('change',e=>{const input=e.target.closest('[data-vlan-select]');if(!input)return;if(input.checked)vlanSelectedPorts.add(input.dataset.vlanSelect);else vlanSelectedPorts.delete(input.dataset.vlanSelect);updateVLANSelection();});
$('vlan-select-all').onclick=()=>{for(const input of $('vlan-port-map').querySelectorAll('[data-vlan-select]')){input.checked=true;vlanSelectedPorts.add(input.dataset.vlanSelect);}updateVLANSelection();};
$('vlan-select-clear').onclick=()=>{vlanSelectedPorts.clear();for(const input of $('vlan-port-map').querySelectorAll('[data-vlan-select]'))input.checked=false;updateVLANSelection();};

async function applyVLANMembership(){
 const ports=[...vlanSelectedPorts].sort(),vlan=Number($('vlan-inspect-id').value),membership=$('vlan-membership-mode').value,output=$('vlan-membership-output'),button=$('vlan-apply-membership');
 if(!ports.length){alert('Zaznacz co najmniej jeden port.');return;}
 if(!Number.isInteger(vlan)||vlan<1||vlan>4094){alert('VLAN ID musi być w zakresie 1–4094.');return;}
 const payload={ports,vlan,membership};button.disabled=true;output.textContent='Sprawdzanie aktualnej konfiguracji portów…';
 try{
  const preview=await api('/api/vlan/ports/preview',payload);
  const summary='PODGLĄD — bez zmian na switchu\n\n'+preview.commands.join('\n')+'\n\n'+preview.warnings.join('\n');
  output.textContent=summary;
  if(!confirm(summary+'\n\nCzy wykonać te komendy?'))return;
  const result=await api('/api/vlan/ports/apply',payload);
  let verificationText='WERYFIKACJA: nie udało się potwierdzić stanu portów.';
  try{
   const check=await api('/api/command',{command:'show vlan '+vlan});
   const observed=new Map();
   for(const match of String(check.output||'').matchAll(/(port\\d+\\.\\d+\\.\\d+)\\s*\\(([ut])\\)/gi))observed.set(match[1],match[2].toLowerCase());
   const expected=membership==='tagged'?'t':'u';
   const rows=ports.map(port=>{const actual=observed.get(port);return {port,actual,ok:actual===expected};});
   const good=rows.filter(row=>row.ok).length;
   verificationText='WERYFIKACJA '+(good===rows.length?'OK':'NIEPEŁNA')+' — '+good+'/'+rows.length+' portów ma oczekiwane członkostwo '+membership.toUpperCase()+' w VLAN '+vlan+'.\\n'+rows.map(row=>row.port+': '+(row.actual==='t'?'TAGGED':row.actual==='u'?'UNTAGGED':'BRAK W TABLICY')+(row.ok?' ✓':' — OCZEKIWANO '+membership.toUpperCase())).join('\\n');
  }catch(checkError){verificationText+='\\n'+checkError.message;}
  output.textContent='WYNIK KOMEND\\n'+(result.output||'Komendy przekazane.')+'\\n\\n'+verificationText;
  addEvent('Zmieniono członkostwo VLAN '+vlan+' na portach '+ports.join(', '));
  alert(verificationText);
  await inspectVLAN();
 }catch(e){output.textContent='Operacja zablokowana lub nieudana: '+e.message;alert(e.message);}
 finally{button.disabled=vlanSelectedPorts.size===0;}
}
$('vlan-apply-membership').onclick=applyVLANMembership;
$('vlan-use-selected').onclick=()=>{const nums=[...vlanSelectedPorts].map(name=>Number(name.match(/\.(\d+)$/)?.[1])).filter(n=>Number.isInteger(n)&&n>=1&&n<=52).sort((a,b)=>a-b);if(!nums.length){alert('Nie znaleziono poprawnych numerów zaznaczonych portów.');return;}$('bulk-ports').value=nums.join(',');$('bulk-port-settings').scrollIntoView({behavior:'smooth',block:'center'});$('bulk-ports').focus();};
let topologyData={rows:[],raw:{}};
const ouiVendors={
 '000c29':'VMware','005056':'VMware','001c42':'Parallels',
 '001b63':'Apple','3c22fb':'Apple','f0d1a9':'Apple','a4b1c1':'Apple','acde48':'Apple','f0dbf8':'Apple',
 '001b21':'Intel','001f3b':'Intel','3c970e':'Intel','a4c494':'Intel','b4d5bd':'Intel',
 '001b54':'Cisco','001e49':'Cisco','0024f7':'Cisco','f4cfe2':'Cisco',
 '0418d6':'Ubiquiti','245a4c':'Ubiquiti','687d6b':'Ubiquiti','802aa8':'Ubiquiti','d021f9':'Ubiquiti',
 '50c7bf':'TP-Link','98dac4':'TP-Link','b0487a':'TP-Link','ec086b':'TP-Link',
 'b827eb':'Raspberry Pi','dca632':'Raspberry Pi','e45f01':'Raspberry Pi',
 '3cd92b':'HPE','001a4b':'HPE','001f29':'HPE','2c233a':'HPE',
 '001a8a':'Samsung','002454':'Samsung','8c7712':'Samsung',
 '001e68':'Dell','14feb5':'Dell','f8bc12':'Dell',
 '0015c5':'Sony','001d0d':'Sony'
};
function macVendor(mac){const key=normalizeMAC(mac).replace(/:/g,'').slice(0,6);return ouiVendors[key]||'Nieznany producent';}
function normalizeMAC(value){const hex=String(value||'').replace(/[^0-9a-f]/gi,'').toLowerCase();return hex.length===12?hex.match(/.{2}/g).join(':'):'';}
function parseFDB(raw){
 const rows=[],macPattern=/(?:[0-9a-f]{4}\.){2}[0-9a-f]{4}|(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}|\b[0-9a-f]{12}\b/ig,portPattern=/\bport\d+\.\d+\.\d+\b/i;
 for(const line of String(raw||'').split(/\r?\n/)){const mm=line.match(macPattern),pm=line.match(portPattern);if(!mm||!pm)continue;const mac=normalizeMAC(mm[0]);if(!mac)continue;const beforeMAC=line.slice(0,line.toLowerCase().indexOf(mm[0].toLowerCase())).trim(),vlanMatch=beforeMAC.match(/^(?:vlan\s*)?(\d{1,4})$/i);rows.push({mac,port:pm[0],vlan:vlanMatch?vlanMatch[1]:'',source:line.trim()});}
 return rows;
}
function parseARP(raw){
 const out=new Map(),ipPattern=/\b(?:\d{1,3}\.){3}\d{1,3}\b/g,macPattern=/(?:[0-9a-f]{4}\.){2}[0-9a-f]{4}|(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}|\b[0-9a-f]{12}\b/i;
 for(const line of String(raw||'').split(/\r?\n/)){const ip=line.match(ipPattern),mac=line.match(macPattern);if(!ip||!mac)continue;const key=normalizeMAC(mac[0]);if(key)out.set(key,[...(out.get(key)||[]),ip[0]]);}
 return out;
}
function parseLLDP(raw){
 const byPort=new Map(),lines=String(raw||'').split(/\r?\n/);
 for(let i=0;i<lines.length;i++){const m=lines[i].match(/\bport\d+\.\d+\.\d+\b/i);if(!m)continue;const port=m[0];let block=lines.slice(Math.max(0,i-3),Math.min(lines.length,i+8)).join(' · ').replace(/\s+/g,' ').trim();if(block.length>260)block=block.slice(0,260)+'…';const list=byPort.get(port)||[];if(!list.includes(block))list.push(block);byPort.set(port,list);}
 return byPort;
}
function classifyNeighbor(text){
 const value=String(text||'').toLowerCase();
 const rules=[
  ['switch',/\b(switch|switching|bridge)\b/],
  ['router',/\b(router|routing)\b/],
  ['ap',/\b(access[\s-]?point|wireless|wlan|wifi)\b/],
  ['camera',/\b(camera|ipcam|cctv|surveillance)\b/],
  ['printer',/\b(printer|printing)\b/],
  ['phone',/\b(telephone|phone|voip|sip phone)\b/],
  ['computer',/\b(workstation|desktop|laptop|computer|server)\b/]
 ];
 for(const [kind,pattern] of rules)if(pattern.test(value))return {kind,label:{switch:'Switch',router:'Router',ap:'Access point',camera:'Kamera',printer:'Drukarka',phone:'Telefon',computer:'Komputer'}[kind],evidence:'Jawny opis LLDP'};
 return {kind:'unknown',label:'Nieustalone',evidence:'Brak jednoznacznego opisu LLDP'};
}
function parseSTP(raw){
 const states=new Map();
 for(const line of String(raw||'').split(/\r?\n/)){
  const port=line.match(/\bport\d+\.\d+\.\d+\b/i);
  if(!port)continue;
  const value=line.toLowerCase();
  let state='';
  if(/\b(alternate|discarding|blocking|blocked)\b/.test(value))state='blocked';
  else if(/\b(forwarding|forward)\b/.test(value))state='forwarding';
  if(state)states.set(port[0],state);
 }
 return states;
}
function renderTopology(){
 const q=$('topology-filter').value.trim().toLowerCase(),kind=$('topology-class-filter').value;
 const rows=topologyData.rows.filter(r=>(kind==='all'||r.deviceClass===kind)&&[r.port,r.link,r.vlans,r.macs,r.ips,r.lldp,r.deviceLabel,r.stp,...(r.macs?r.macs.split(', ').map(macVendor):[])].join(' ').toLowerCase().includes(q));
 $('topology-rows').innerHTML=rows.length?rows.map(r=>'<tr><td><button class="port-diagnostic-link" data-topology-port="'+esc(r.port)+'">'+esc(r.port)+'</button></td><td><span class="state '+(r.link==='up'?'up':r.link==='down'?'down':'unknown')+'">'+esc(r.link)+'</span></td><td>'+esc(r.vlans||'—')+'</td><td>'+esc(r.macs||'—')+'</td><td>'+esc(r.ips||'—')+'</td><td>'+esc(r.lldp||'—')+'</td></tr>').join(''):'<tr><td colspan="6" class="empty">Brak danych pasujących do filtra.</td></tr>';
 $('topology-map').innerHTML=rows.length?rows.map(r=>{const macs=r.macs?r.macs.split(', ').filter(Boolean):[],ips=r.ips?r.ips.split(', ').filter(Boolean):[],uplink=r.uplink?' · Prawdopodobny uplink':'';return '<article class="topology-port-card"><div class="topology-port-node"><span class="state '+(r.link==='up'?'up':r.link==='down'?'down':'unknown')+'">'+esc(r.link)+'</span><button data-topology-port="'+esc(r.port)+'">'+esc(r.port)+'</button><small>VLAN '+esc(r.vlans||'—')+'</small><span class="topology-class-badge">'+esc(r.deviceLabel)+'</span>'+(r.uplink?'<span class="topology-hint">Prawdopodobny uplink</span>':'')+(r.neighborCount>1?'<span class="topology-multi-neighbor">Wiele bloków LLDP: '+r.neighborCount+'</span>':'')+(r.stp==='blocked'?'<span class="topology-stp-blocked">STP: blokowany</span>':r.stp==='forwarding'?'<span class="topology-stp-forwarding">STP: forwarding</span>':'')+'</div><div class="topology-device-list">'+(macs.length?macs.map(mac=>{const vendor=macVendor(mac);return '<div class="topology-device"><b>'+esc(vendor)+'</b><code>'+esc(mac)+'</code><small>'+esc(ips.join(', ')||'Brak adresu IP w ARP')+'</small></div>';}).join(''):'<div class="topology-device empty-device">Brak wpisu FDB dla tego portu</div>')+(r.lldp?'<div class="topology-lldp"><small>LLDP · '+esc(r.deviceEvidence)+'</small>'+esc(r.lldp)+'</div>':'')+'</div></article>';}).join(''):'<div class="empty">Brak danych pasujących do filtra.</div>';
 const candidates=topologyData.rows.filter(r=>r.uplink),blocked=candidates.filter(r=>r.stp==='blocked'),forwarding=candidates.filter(r=>r.stp==='forwarding');
 $('topology-redundancy').textContent=candidates.length>=2?(blocked.length&&forwarding.length?'Wykryto wiele prawdopodobnych uplinków, w tym STP blokowany i forwarding. To może wskazywać na redundancję ścieżek; nie jest automatycznym potwierdzeniem pętli.':'Wykryto '+candidates.length+' prawdopodobne uplinki LLDP. Sprawdź LACP i STP; sama liczba uplinków nie potwierdza redundancji ani pętli.'):'Brak wystarczających danych LLDP do oceny redundancji. Brak wskazania nie wyklucza połączeń zapasowych.';
}
async function inspectTopologyVLAN(){
 const id=Number($('topology-vlan-id').value),box=$('topology-vlan-map'),status=$('topology-vlan-status'),button=$('topology-vlan-load');
 if(!Number.isInteger(id)||id<1||id>4094){status.textContent='VLAN ID musi być w zakresie 1–4094.';return;}
 button.disabled=true;status.textContent='Odczyt członkostwa VLAN '+id+'…';box.innerHTML='<div class="empty">Pobieranie danych…</div>';
 try{
  const d=await api('/api/command',{command:'show vlan '+id}),raw=d.output||'',members=new Map();
  for(const m of raw.matchAll(/(port\d+\.\d+\.\d+)\s*\(([ut])\)/gi))members.set(m[1],m[2].toLowerCase());
  const rows=latestPorts.map(p=>({port:p.name,mode:members.get(p.name)||'none',device:topologyData.rows.find(r=>r.port===p.name)}));
  box.innerHTML=rows.map(x=>{
   const r=x.device,macs=r&&r.macs?r.macs.split(', ').filter(Boolean):[],ips=r&&r.ips?r.ips.split(', ').filter(Boolean):[];
   return '<article class="topology-vlan-port"><div class="topology-vlan-port-head"><button class="port-diagnostic-link" data-topology-port="'+esc(x.port)+'">'+esc(x.port)+'</button><span class="vlan-member '+(x.mode==='u'?'untagged':x.mode==='t'?'tagged':'none')+'">'+(x.mode==='u'?'UNTAGGED':x.mode==='t'?'TAGGED':'POZA VLAN / BRAK DANYCH')+'</span></div><div class="topology-vlan-devices">'+(macs.length?macs.map(mac=>'<div><b>'+esc(macVendor(mac))+'</b><code>'+esc(mac)+'</code></div>').join(''):'<small>Brak wpisów MAC na tym porcie</small>')+(ips.length?'<small>IP: '+esc(ips.join(', '))+'</small>':'')+(r&&r.lldp?'<small>LLDP: '+esc(r.lldp)+'</small>':'')+'</div></article>';
  }).join('');
  status.textContent='VLAN '+id+' · '+[...members.values()].filter(x=>x==='u').length+' portów untagged · '+[...members.values()].filter(x=>x==='t').length+' portów tagged · dane urządzeń pochodzą z ostatniego skanowania topologii.';
  if(!members.size)status.textContent+=' Nie rozpoznano członkostwa w formacie CLI — sprawdź odpowiedź surową w module VLAN.';
 }catch(e){status.textContent='Błąd odczytu VLAN: '+e.message;box.innerHTML='<div class="empty">Nie udało się pobrać mapy VLAN.</div>';}
 finally{button.disabled=false;}
}
$('topology-vlan-load').onclick=inspectTopologyVLAN;
$('topology-vlan-map').addEventListener('click',e=>{const b=e.target.closest('[data-topology-port]');if(b)openPortDiagnostics(b.dataset.topologyPort);});
async function refreshTopology(){
 const button=$('refresh-topology');button.disabled=true;button.textContent='Skanowanie…';$('topology-status').textContent='Pobieranie tablic FDB, ARP, LLDP i VLAN…';
 const commands=[['fdb','show mac address-table'],['arp','show arp'],['lldp','show lldp neighbors'],['vlan','show vlan all'],['switchport','show interface switchport'],['stp','show spanning-tree']];
 try{
  const results=await Promise.all(commands.map(async([key,command])=>{try{const d=await api('/api/command',{command});return [key,{output:d.output||''}];}catch(e){return [key,{error:e.message,output:''}];}}));
  const raw=Object.fromEntries(results);topologyData.raw=raw;
  for(const key of ['fdb','arp','lldp','vlan','stp'])$('topology-raw-'+key).textContent=raw[key].error?'Błąd: '+raw[key].error:raw[key].output||'Brak danych.';
  const fdb=parseFDB(raw.fdb.output),arp=parseARP(raw.arp.output),lldp=parseLLDP(raw.lldp.output),stp=parseSTP(raw.stp.output),byPort=new Map();
  for(const p of latestPorts)byPort.set(p.name,{port:p.name,link:p.oper||'unknown',vlans:new Set(),macs:new Set(),ips:new Set(),neighbors:new Set()});
  for(const entry of fdb){if(!byPort.has(entry.port))byPort.set(entry.port,{port:entry.port,link:'unknown',vlans:new Set(),macs:new Set(),ips:new Set(),neighbors:new Set()});const row=byPort.get(entry.port);row.macs.add(entry.mac);if(entry.vlan)row.vlans.add(entry.vlan);for(const ip of arp.get(entry.mac)||[])row.ips.add(ip);}
  for(const [port,items] of lldp){if(!byPort.has(port))byPort.set(port,{port,link:'unknown',vlans:new Set(),macs:new Set(),ips:new Set(),neighbors:new Set()});for(const item of items)byPort.get(port).neighbors.add(item);}
  topologyData.rows=[...byPort.values()].map(r=>({port:r.port,link:r.link,vlans:[...r.vlans].join(', '),macs:[...r.macs].join(', '),ips:[...r.ips].join(', '),lldp:[...r.neighbors].join(' | '),neighborCount:r.neighbors.size})).map(r=>{const device=classifyNeighbor(r.lldp);r.deviceClass=device.kind;r.deviceLabel=device.label;r.deviceEvidence=device.evidence;r.stp=stp.get(r.port)||'unknown';r.uplink=r.link==='up'&&['switch','router'].includes(device.kind);return r;}).sort((a,b)=>natural(a.port,b.port));
  $('topology-port-count').textContent=topologyData.rows.length;$('topology-mac-count').textContent=fdb.length;$('topology-ip-count').textContent=[...arp.values()].reduce((n,x)=>n+x.length,0);$('topology-lldp-count').textContent=[...lldp.values()].reduce((n,x)=>n+x.length,0);
  const errors=results.filter(x=>x[1].error).map(x=>x[0]+': '+x[1].error);
  $('topology-status').textContent='Odczyt '+new Date().toLocaleString()+' · FDB: '+fdb.length+' wpisów · ARP: '+[...arp.values()].reduce((n,x)=>n+x.length,0)+' adresów · LLDP: '+[...lldp.values()].reduce((n,x)=>n+x.length,0)+' obserwacji'+(errors.length?' · Błędy: '+errors.join('; '):'');
  renderTopology();
 }finally{button.disabled=false;button.textContent='Skanuj ponownie';}
}
$('refresh-topology').onclick=refreshTopology;
$('topology-filter').addEventListener('input',renderTopology);$('topology-class-filter').addEventListener('change',renderTopology);
$('topology-rows').addEventListener('click',e=>{const b=e.target.closest('[data-topology-port]');if(b)openPortDiagnostics(b.dataset.topologyPort);});
async function refreshDHCP(){for(const [cmd,id] of [['show ip dhcp server summary','dhcp-summary'],['show ip dhcp binding','dhcp-bindings']]){try{const d=await api('/api/command',{command:cmd});$(id).textContent=d.output||'Brak danych';}catch(e){$(id).textContent=e.message;}}}
$('dhcp-refresh').onclick=refreshDHCP;

let arpTableText='';
async function refreshL3(){for(const [cmd,id] of [['show ip interface brief','l3-interfaces'],['show ip route','l3-routes'],['show arp','l3-arp']]){try{const d=await api('/api/command',{command:cmd});const out=d.output||'Brak danych zwróconych przez switch.';$(id).textContent=out;if(id==='l3-arp'){arpTableText=out;filterARP();}}catch(e){$(id).textContent=e.message;if(id==='l3-arp'){arpTableText=e.message;filterARP();}}}}
function filterARP(){const q=$('arp-filter').value.trim().toLowerCase();$('l3-arp').textContent=q?arpTableText.split(/\r?\n/).filter(x=>x.toLowerCase().includes(q)).join('\n')||'Brak pasujących wpisów.':arpTableText;}

async function configureStaticRoute(remove){const q={prefix:$('route-prefix').value.trim(),gateway:$('route-gateway').value.trim(),distance:Number($('route-distance').value),delete:remove};const action=remove?'usunąć':'dodać';if(!confirm('Czy na pewno '+action+' trasę '+q.prefix+' przez '+q.gateway+'? Zmiana może wpłynąć na łączność z przełącznikiem.'))return;try{const d=await api('/api/route/static',q);$('probe-output').textContent=d.output||'Polecenie przekazane.';await refreshL3();}catch(e){alert(e.message);}}
$('route-add').onclick=()=>configureStaticRoute(false);
$('route-delete').onclick=()=>configureStaticRoute(true);
async function runProbe(kind){const target=$('probe-target').value.trim();$('probe-output').textContent=(kind==='ping'?'Ping':'Traceroute')+' '+target+'…';try{const d=await api('/api/network/probe',{kind,target});$('probe-output').textContent=d.output||'Brak odpowiedzi.';}catch(e){$('probe-output').textContent=e.message;}}
$('probe-ping').onclick=()=>runProbe('ping');
$('probe-trace').onclick=()=>runProbe('traceroute');

async function refreshSNMP(){const jobs=[['show snmp-server','snmp-agent'],['show snmp-server community','snmp-communities'],['show snmp-server user','snmp-users'],['show counter snmp-server','snmp-counters'],['show running-config snmp','snmp-running']];for(const [command,id] of jobs){try{const d=await api('/api/command',{command});$(id).textContent=d.output||'Brak danych zwróconych przez switch.';}catch(e){$(id).textContent=e.message;}}}
$('snmp-refresh').onclick=refreshSNMP;

async function refreshSSH(){for(const [command,id] of [['show users','ssh-users'],['show ssh server','ssh-status'],['show running-config ssh','ssh-config']]){try{const d=await api('/api/command',{command});$(id).textContent=d.output||'Brak danych.';}catch(e){$(id).textContent=e.message;}}}
$('ssh-refresh').onclick=refreshSSH;
async function changeSSHUser(remove){const q={username:$('ssh-new-user').value.trim(),password:$('ssh-new-password').value,privilege:Number($('ssh-new-privilege').value),delete:remove};if(!q.username){alert('Wpisz login.');return;}if(!remove&&!q.password){alert('Wpisz nowe hasło.');return;}if(!confirm((remove?'Usunąć':'Utworzyć lub zaktualizować')+' konto '+q.username+'?'))return;const b=remove?$('ssh-user-delete'):$('ssh-user-save');b.disabled=true;try{const d=await api('/api/ssh/user',q);$('ssh-action-output').textContent=d.output||'Polecenie przekazane.';$('ssh-new-password').value='';await refreshSSH();}catch(e){$('ssh-action-output').textContent=e.message;}finally{b.disabled=false;}}
$('ssh-user-save').onclick=()=>changeSSHUser(false);
$('ssh-user-delete').onclick=()=>changeSSHUser(true);

async function changeSNMPTrap(remove){const q={address:$('snmp-trap-ip').value.trim(),community:$('snmp-trap-community').value,delete:remove};if(!q.community){alert('Wpisz istniejącą community SNMP.');return;}if(!confirm((remove?'Usunąć':'Dodać')+' odbiorcę Trap '+q.address+' dla SNMPv2c?'))return;const b=remove?$('snmp-trap-remove'):$('snmp-trap-add');b.disabled=true;try{const d=await api('/api/snmp/trap-host',q);$('snmp-action-output').textContent=d.output||'Polecenie wysłane.';await refreshSNMP();}catch(e){$('snmp-action-output').textContent=e.message;}finally{b.disabled=false;}}
$('snmp-trap-add').onclick=()=>changeSNMPTrap(false);
$('snmp-trap-remove').onclick=()=>changeSNMPTrap(true);
async function changeSNMPv3User(remove){
 const q={username:$('snmp-v3-user').value.trim(),group:$('snmp-v3-group').value.trim(),authPassword:$('snmp-v3-auth').value,privacyPassword:$('snmp-v3-privacy').value,delete:remove};
 if(!q.username){alert('Wpisz nazwę użytkownika SNMPv3.');return;}
 if(!remove&&(!q.authPassword||!q.privacyPassword)){alert('Wpisz oba hasła SNMPv3.');return;}
 if(!confirm((remove?'Usunąć':'Utworzyć')+' użytkownika SNMPv3 '+q.username+(remove?'?':' z SHA + AES (authPriv)?')))return;
 const b=remove?$('snmp-v3-remove'):$('snmp-v3-add');b.disabled=true;
 try{const d=await api('/api/snmp/v3-user',q);$('snmp-v3-output').textContent=d.output||'Polecenie wysłane.';$('snmp-v3-auth').value='';$('snmp-v3-privacy').value='';await refreshSNMP();}
 catch(e){$('snmp-v3-output').textContent=e.message;}
 finally{b.disabled=false;}
}
$('snmp-v3-add').onclick=()=>changeSNMPv3User(false);
$('snmp-v3-remove').onclick=()=>changeSNMPv3User(true);
async function changeSNMPv3Trap(remove){
 const q={address:$('snmp-v3-trap-ip').value.trim(),username:$('snmp-v3-trap-user').value.trim(),delete:remove};
 if(!q.address||!q.username){alert('Wpisz adres IPv4 i użytkownika SNMPv3.');return;}
 if(!confirm((remove?'Usunąć':'Dodać')+' odbiorcę Trap SNMPv3 '+q.address+' ('+q.username+')?'))return;
 const b=remove?$('snmp-v3-trap-remove'):$('snmp-v3-trap-add');b.disabled=true;
 try{const d=await api('/api/snmp/v3-trap-host',q);$('snmp-v3-trap-output').textContent=d.output||'Polecenie wysłane.';await refreshSNMP();}
 catch(e){$('snmp-v3-trap-output').textContent=e.message;}
 finally{b.disabled=false;}
}
$('snmp-v3-trap-add').onclick=()=>changeSNMPv3Trap(false);
$('snmp-v3-trap-remove').onclick=()=>changeSNMPv3Trap(true);


$('refresh-l3').onclick=refreshL3;
$('arp-filter').addEventListener('input',filterARP);

$('dhcp-create').onclick=async()=>{const q={name:$('dhcp-name').value,network:$('dhcp-network').value,mask:$('dhcp-mask').value,low:$('dhcp-low').value,high:$('dhcp-high').value,router:$('dhcp-router').value,dns:$('dhcp-dns').value,enable:true};if(!confirm('Utworzyć pulę DHCP '+q.name+' i włączyć usługę DHCP Server? Upewnij się, że nie działa drugi serwer DHCP w tej sieci.'))return;const b=$('dhcp-create');b.disabled=true;try{const d=await api('/api/dhcp/pool',q);$('dhcp-summary').textContent=d.output||'Polecenia konfiguracji wysłane. Odczytaj status DHCP.';await refreshDHCP();}catch(e){alert(e.message);}finally{b.disabled=false;}};

async function loadDiagnostic(command,target){$(target).textContent='Pobieranie przez SSH…';try{const d=await api('/api/command',{command});$(target).textContent=d.output||'Brak danych';}catch(e){$(target).textContent=e.message;}}
$('load-system').onclick=async()=>{await loadDiagnostic('show system','system-output');const env=await api('/api/command',{command:'show system environment'}).catch(e=>({error:e.message}));$('system-output').textContent+='\n\n--- show system environment ---\n'+(env.output||env.error||'Brak danych');};

let macFDBText='';
async function refreshFDB(){try{const d=await api('/api/command',{command:'show mac address-table'});macFDBText=d.output||'';$('mac-output').textContent=macFDBText;$('mac-table-output').textContent=macFDBText||'Switch nie zwrócił wpisów FDB.';filterFDB();}catch(e){$('mac-output').textContent=e.message;$('mac-table-output').textContent=e.message;}}
function filterFDB(){const q=$('mac-filter').value.trim().toLowerCase();const lines=macFDBText.split(/\r?\n/);$('mac-table-output').textContent=q?lines.filter(x=>x.toLowerCase().includes(q)).join('\n')||'Brak pasujących wpisów.':macFDBText||'Brak danych.';}
$('load-mac').onclick=refreshFDB;
$('mac-filter').addEventListener('input',filterFDB);
$('load-lldp').onclick=async()=>{try{const d=await api('/api/command',{command:'show lldp neighbors'});$('lldp-output').textContent=d.output||'Brak wykrytych sąsiadów LLDP.';}catch(e){$('lldp-output').textContent=e.message;}};

$('load-logs').onclick=()=>loadDiagnostic('show log tail 30','logs-output');
$('create-vlan').onclick=async()=>{const id=Number($('vlan-id').value),name=$('vlan-name').value;try{const p=await api('/api/vlan/preview',{id,name,delete:false});const summary='PODGLĄD — bez zmian na switchu\\n\\n'+p.commands.join('\n')+'\n\\n'+p.warnings.join('\n');$('vlan-output').textContent=summary;if(!confirm(summary+'\n\\nWysłać te komendy do przełącznika?'))return;const d=await api('/api/vlan',{id,name,delete:false});$('vlan-output').textContent=d.output||'Polecenie przekazane';alert('Polecenie utworzenia VLAN wysłane. Zweryfikuj listę VLAN.');}catch(e){alert(e.message);}};
$('delete-vlan').onclick=async()=>{const id=Number($('vlan-id').value);try{const p=await api('/api/vlan/preview',{id,delete:true});const summary='PODGLĄD — bez zmian na switchu\\n\\n'+p.commands.join('\n')+'\n\\n'+p.warnings.join('\n');$('vlan-output').textContent=summary;if(!confirm(summary+'\n\\nCzy na pewno wykonać?'))return;const d=await api('/api/vlan',{id,delete:true});$('vlan-output').textContent=d.output||'Polecenie przekazane';}catch(e){alert(e.message);}};
$('show-vlans').onclick=async()=>{try{const d=await api('/api/command',{command:'show vlan all'});$('vlan-output').textContent=d.output||'Brak danych';}catch(e){$('vlan-output').textContent=e.message;}};
$('apply-port-settings').onclick=async()=>{const payload={port:$('cfg-port').value,mode:$('cfg-mode').value,accessVlan:Number($('cfg-access-vlan').value),allowed:$('cfg-allowed').value,native:Number($('cfg-native').value),speed:$('cfg-speed').value,duplex:$('cfg-duplex').value,description:$('cfg-description').value};if(!payload.port){alert('Wybierz port.');return;}try{const p=await api('/api/port/preview',payload);const summary='PODGLĄD — bez zmian na switchu\\n\\n'+p.commands.join('\n')+'\n\\n'+p.warnings.join('\n');$('output').textContent=summary;if(!confirm(summary+'\n\\nWysłać te komendy do przełącznika?'))return;const d=await api('/api/port/settings',payload);$('output').textContent=d.output||'Polecenie przekazane';addEvent('Zmieniono konfigurację '+payload.port);alert('Komendy przekazane. Zweryfikuj konfigurację portu w CLI.');$('port-dialog').close();await load();}catch(e){alert(e.message);}};
$('detail-port').addEventListener('change',renderPortDetail);
$('load-port-network').addEventListener('click',loadPortNetworkContext);
$('port-map').addEventListener('click',e=>{const b=e.target.closest('[data-map-port]');if(b)openPortDiagnostics(b.dataset.mapPort);});
$('top-ports').addEventListener('click',e=>{const b=e.target.closest('[data-diagnostic-port]');if(b)openPortDiagnostics(b.dataset.diagnosticPort);});
$('refresh').onclick=load;$('filter').oninput=render;$('state-filter').onchange=render;
$('ports').addEventListener('click',async e=>{const diagnostic=e.target.closest('[data-config-port]');if(diagnostic)return;const b=e.target.closest('[data-port]');if(!b)return;const action=b.dataset.action,port=b.dataset.port;if(!confirm((action==='disable'?'Wyłączyć':'Włączyć')+' port '+port+'?'))return;b.disabled=true;try{const d=await api('/api/port',{port,action});$('output').textContent=d.output||'Polecenie wysłane';addEvent('Port '+port+' — '+(action==='disable'?'wyłączono':'włączono'));await load();}catch(x){alert('Nie udało się zmienić portu: '+x.message);}finally{b.disabled=false;}});
$('dhcp-toggle').onchange=async e=>{const enabled=e.target.checked;if(!confirm((enabled?'Włączyć':'Wyłączyć')+' klienta DHCP na VLAN 1? Zmiana adresu może zerwać połączenie.')){e.target.checked=!enabled;return;}e.target.disabled=true;try{const d=await api('/api/management-dhcp',{enabled});$('output').textContent=d.output||'Polecenie wysłane';alert('Polecenie przekazane. Sprawdź adres zarządzający switcha.');}catch(x){e.target.checked=!enabled;alert(x.message);}finally{e.target.disabled=false;}};
async function refreshAudit(){const body=$('audit-rows');body.innerHTML='<tr><td colspan="6" class="empty">Odczyt dziennika…</td></tr>';try{const items=await api('/api/audit');body.innerHTML=items.length?items.slice().reverse().map(x=>{const status=Number(x.status)||0;return '<tr><td>'+esc(new Date(x.timestamp).toLocaleString())+'</td><td>'+esc(x.username)+'</td><td><b>'+esc(x.method)+'</b></td><td><code>'+esc(x.path)+'</code></td><td><span class="state '+(status>=500?'down':status>=400?'unknown':'up')+'">'+status+'</span></td><td>'+fmtCount(x.durationMs)+' ms</td></tr>';}).join(''):'<tr><td colspan="6" class="empty">Brak zarejestrowanych operacji.</td></tr>';}catch(e){body.innerHTML='<tr><td colspan="6" class="empty">'+esc(e.message)+'</td></tr>';}}
$('refresh-audit').onclick=refreshAudit;
function renderConfigBackups(items){const el=$('config-backups');if(!items.length){el.textContent='Brak zapisanych kopii.';return;}el.innerHTML=items.map(x=>'<div class="health-line"><span><b>'+esc(new Date(x.createdAt).toLocaleString())+'</b><small> · '+fmtCount(x.size)+' B · SHA256 '+esc(x.sha256.slice(0,16))+'…</small><br><small class="'+(x.verified?'good':'bad')+'">'+(x.verified?'SHA-256 zweryfikowany':'NIEZWERYFIKOWANA KOPIA')+'</small></span><a class="btn ghost" href="/api/config/backups/download?id='+encodeURIComponent(x.id)+'">Pobierz</a></div>').join('');}
async function refreshConfigBackups(){const el=$('config-backups');el.textContent='Odczyt historii kopii…';try{const r=await fetch('/api/config/backups');if(!r.ok)throw new Error('HTTP '+r.status);renderConfigBackups(await r.json());}catch(e){el.textContent='Nie udało się odczytać historii: '+e.message;}}
$('refresh-config-backups').onclick=refreshConfigBackups;
$('create-config-backup').onclick=async()=>{const b=$('create-config-backup');if(!confirm('Pobrać running-config i zapisać lokalną kopię na serwerze panelu? Plik może zawierać hasła i inne dane poufne.'))return;b.disabled=true;try{const r=await fetch('/api/config/backups',{method:'POST'});if(!r.ok)throw new Error('HTTP '+r.status);const d=await r.json();$('config-diff').textContent='Utworzono kopię '+d.id+'\nSHA256: '+d.sha256;await refreshConfigBackups();}catch(e){$('config-diff').textContent='Nie udało się utworzyć kopii: '+e.message;}finally{b.disabled=false;}};
$('compare-config').onclick=async()=>{const box=$('config-diff'),button=$('compare-config');button.disabled=true;box.textContent='Pobieranie running-config i startup-config…';try{const response=await fetch('/api/config/compare');if(!response.ok)throw new Error('Błąd porównania: HTTP '+response.status);const d=await response.json();const out=[];out.push('Różnice: '+d.added.length+' dodanych w running-config, '+d.removed.length+' usuniętych względem startup-config.');out.push('Linie: running '+d.runningLines+' · startup '+d.startupLines);out.push('');out.push('DODANE / ZMIENIONE W RUNNING-CONFIG:');out.push(...(d.added.length?d.added.map(x=>'+ '+x):['(brak)']));out.push('');out.push('OBECNE W STARTUP-CONFIG, BRAK W RUNNING-CONFIG:');out.push(...(d.removed.length?d.removed.map(x=>'- '+x):['(brak)']));box.textContent=out.join('\n');}catch(e){box.textContent=e.message;}finally{button.disabled=false;}};
$('save-config').onclick=async()=>{if(!confirm('Zapisać running-config do startup-config?'))return;try{const d=await api('/api/save',{});$('output').textContent=d.output||'Konfiguracja zapisana';alert('Polecenie zapisu wysłane.');}catch(x){alert(x.message);}};
$('run').onclick=async()=>{const command=$('cmd').value;$('output').textContent='Łączenie przez SSH…';try{const d=await api('/api/command',{command});$('output').textContent=d.output||'Brak wyniku';}catch(x){$('output').textContent=x.message;}};load();setInterval(load,15000);restoreMonitorHistory();refreshConfigBackups();refreshAudit();
$('logout').onclick=async()=>{try{await fetch('/api/logout',{method:'POST',credentials:'same-origin'});}finally{location.href='/login';}};
