function showFriendlyResult(id,raw,label){
 const el=$(id);if(!el)return;
 const value=String(raw||'').trim();
 const lines=value.split(/\r?\n/).map(x=>x.trim()).filter(Boolean);
 const failed=/timed out|przekroczono|permission denied|authentication failed|not supported|nie udało|odmowa|failed|error:/i.test(value);
 const empty=!value||/no entries|no records|not found|empty|brak wpis|brak danych|brak odpowiedzi|nie wykryto|no data|no .* found/i.test(value);
 const count=lines.filter(x=>!/^[-=+| ]+$/.test(x)&&!/^\s*(vlan|port|interface|address|protocol|destination|neighbor|mac)\b/i.test(x)).length;
 el.classList.add('friendly-result');
 el.innerHTML='<div class="friendly-result-icon '+(failed?'is-muted':empty?'is-muted':'is-ok')+'">'+(failed?'—':empty?'○':'✓')+'</div><div class="friendly-result-copy"><b>'+esc(failed?'Nie udało się odczytać':empty?'Brak aktywnych wpisów':label+' — odczytano')+'</b><span>'+esc(failed?'Sprawdź łączność z urządzeniem i uprawnienia odczytu.':empty?'Switch nie zgłosił obecnie danych w tej kategorii.':'Odczyt zakończony poprawnie. Wykryto '+count+' pozycji.')+'</span></div><span class="friendly-result-count">'+(failed||empty?'—':fmtCount(count))+'</span>';
}
const $=id=>document.getElementById(id);let latestPorts=[];let portNetworkContext=new Map();let trafficHistory=[];let previousCounters=null;let previousAt=0;let previousPortCounters=new Map();let portRates=new Map();
const portTrafficHistory=new Map();let recentEvents=[];let portHistory=[];let counterHistory=[];let previousPortStates=new Map();let previousErrorCounters=new Map();let previousReachable=null;let hasPortBaseline=false;
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const natural=(a,b)=>String(a??'').localeCompare(String(b??''),'en',{numeric:true,sensitivity:'base'});
const sorted=ports=>[...ports].sort((a,b)=>natural(a.name,b.name)||Number(a.index)-Number(b.index));
function initThemeToggle(){
 const button=$('theme-toggle');
 const root=document.documentElement;
 let saved='dark';
 try{saved=localStorage.getItem('switch-panel-theme')||'dark';}catch(_e){}
 const apply=theme=>{
  const light=theme==='light';
  root.dataset.theme=light?'light':'dark';
  if(button){
   button.textContent=light?'☾ Ciemny motyw':'☼ Jasny motyw';
   button.setAttribute('aria-label',light?'Przełącz na ciemny motyw':'Przełącz na jasny motyw');
   button.setAttribute('aria-pressed',String(light));
   button.title=light?'Włącz ciemny motyw':'Włącz jasny motyw';
  }
 };
 apply(saved);
 if(button)button.addEventListener('click',()=>{
  const next=root.dataset.theme==='light'?'dark':'light';
  apply(next);
  try{localStorage.setItem('switch-panel-theme',next);}catch(_e){}
 });
}
initThemeToggle();
async function api(path,body,method){const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),30000);let r;try{r=await fetch(path,{method:method||(body===undefined?'GET':'POST'),headers:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),cache:'no-store',signal:controller.signal});}catch(e){if(e.name==='AbortError')throw Error('Przekroczono limit czasu odpowiedzi API (30 s): '+path);throw e;}finally{clearTimeout(timer);}const raw=await r.text();let d={};try{d=raw?JSON.parse(raw):{};}catch(_e){d={error:raw||'Nieprawidłowa odpowiedź API'};}if(r.status===401){location.href='/login';throw Error('Sesja wygasła — zaloguj się ponownie.');}if(!r.ok)throw Error(d.error||'Błąd API');return d;}

function activatePage(hash){
 // Moduły (ustawienia, front na żywo) reagują na zmianę widoku; menu używa replaceState, które nie wywołuje hashchange.
 setTimeout(()=>document.dispatchEvent(new CustomEvent('page:change',{detail:{hash:hash||'#overview'}})),0);
 const main=document.querySelector('.main');if(!main)return;
 const targetId=(hash||'#overview').slice(1),target=document.getElementById(targetId);
 const topbar=main.querySelector('.topbar');
 [...main.children].forEach(node=>{if(node!==topbar&&node.id!=='port-dialog')node.hidden=true;});
 document.querySelectorAll('.sidebar .nav').forEach(a=>a.classList.toggle('active',a.getAttribute('href')==='#'+targetId));
 if(targetId==='overview'){
  ['overview','faceplate','warning','network-dashboard'].forEach(id=>{const el=document.getElementById(id);if(el)el.hidden=false;});
  const metrics=main.querySelector('.metrics');if(metrics)metrics.hidden=false;
  return;
 }
 if(!target||!main.contains(target)){activatePage('#overview');return;}
 const branch=[];
 let node=target;
 while(node&&node!==main){branch.push(node);node=node.parentElement;}
 if(node!==main){activatePage('#overview');return;}
 for(let i=0;i<branch.length;i++){
  const current=branch[i];
  current.hidden=false;
  const parent=branch[i+1];
  if(parent&&parent!==main){
   for(const sibling of parent.children){sibling.hidden=sibling!==current;}
   parent.hidden=false;
  }
 }
 target.querySelectorAll('[hidden]').forEach(el=>{el.hidden=false;});
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

function syncPortChoices(){for(const id of ['cfg-port','edgeport-port','detail-port','bulk-source-port','compare-port-a','compare-port-b','compliance-ports']){const sel=$(id);const old=sel.multiple?[...sel.selectedOptions].map(o=>o.value):[sel.value];sel.innerHTML=latestPorts.map(p=>'<option value="'+esc(p.name)+'">'+esc(p.name)+'</option>').join('');for(const option of sel.options)option.selected=old.includes(option.value);}}
$('bulk-profile').onchange=()=>{
 const profile=$('bulk-profile').value;
 if(!profile)return;
 $('bulk-mode').value=profile;
 if(profile==='access'){
  $('bulk-allowed').value='';
  $('bulk-allowed').disabled=true;
  $('bulk-access-vlan').disabled=false;
  $('bulk-native').disabled=true;
 }else{
  $('bulk-allowed').disabled=false;
  $('bulk-access-vlan').disabled=true;
  $('bulk-native').disabled=false;
 }
 $('bulk-output').textContent='Wybrano szablon '+(profile==='access'?'Access':'Trunk')+'. To tylko wstępne ustawienie formularza — przed zmianą sprawdź VLAN-y i porty.';
};
$('bulk-mode').onchange=()=>{
 const trunk=$('bulk-mode').value==='trunk';
 $('bulk-profile').value='';
 $('bulk-access-vlan').disabled=trunk;
 $('bulk-allowed').disabled=!trunk;
 $('bulk-native').disabled=!trunk;
};

let savedPortProfiles=[];
function renderSavedPortProfiles(){
 const select=$('saved-port-profile'),selected=select.value;
 select.innerHTML='<option value="">Wybierz zapisany profil…</option>'+savedPortProfiles.map(p=>'<option value="'+esc(p.id)+'">'+esc(p.name)+' · '+esc(p.mode.toUpperCase())+'</option>').join('');
 if(savedPortProfiles.some(p=>p.id===selected))select.value=selected;
}
async function refreshSavedPortProfiles(){
 const status=$('saved-profile-status'),button=$('refresh-port-profiles');
 if(button)button.disabled=true;
 if(status)status.textContent='Pobieranie biblioteki profili…';
 try{
  const data=await api('/api/port-profiles');
  savedPortProfiles=Array.isArray(data.profiles)?data.profiles:[];
  renderSavedPortProfiles();
  renderComplianceProfiles();
  if(status)status.textContent='Zapisane profile: '+savedPortProfiles.length+' / 100. Dane przechowywane lokalnie na serwerze panelu.';
 }catch(e){if(status)status.textContent='Nie udało się pobrać profili: '+e.message;}
 finally{if(button)button.disabled=false;}
}
function selectedSavedPortProfile(){return savedPortProfiles.find(p=>p.id===$('saved-port-profile').value)||null;}
$('refresh-port-profiles').onclick=refreshSavedPortProfiles;
$('load-port-profile').onclick=()=>{
 const p=selectedSavedPortProfile();if(!p){alert('Wybierz zapisany profil.');return;}
 $('bulk-mode').value=p.mode;$('bulk-profile').value='';
 $('bulk-mode').onchange();
 $('bulk-access-vlan').value=p.accessVlan||1;$('bulk-allowed').value=p.allowed||'';$('bulk-native').value=p.native||1;
 $('bulk-speed').value=p.speed||'auto';$('bulk-duplex').value=p.duplex||'auto';$('bulk-description').value=p.portDescription||'';
 $('saved-profile-name').value=p.name;$('saved-profile-description').value=p.description||'';
 $('bulk-output').textContent='Wczytano profil „'+p.name+'” do formularza. Sprawdź porty docelowe i podgląd zmian przed zastosowaniem.';
};
$('save-port-profile').onclick=async()=>{
 const button=$('save-port-profile'),selected=selectedSavedPortProfile();
 const mode=$('bulk-mode').value;
 const profile={
  ...(selected?{id:selected.id}:{}),
  name:$('saved-profile-name').value.trim(),
  description:$('saved-profile-description').value.trim(),
  portDescription:$('bulk-description').value.trim(),
  mode,
  accessVlan:Number($('bulk-access-vlan').value),
  allowed:$('bulk-allowed').value.trim(),
  native:Number($('bulk-native').value),
  speed:$('bulk-speed').value,
  duplex:$('bulk-duplex').value
 };
 if(!profile.name){alert('Podaj nazwę profilu.');return;}
 button.disabled=true;
 try{
  const result=await api('/api/port-profiles',profile);
  savedPortProfiles=Array.isArray(result.profiles)?result.profiles:[];
  renderSavedPortProfiles();renderComplianceProfiles();$('saved-port-profile').value=result.profile.id;$('compliance-profile').value=result.profile.id;
  $('saved-profile-status').textContent='Profil „'+result.profile.name+'” został zapisany na serwerze panelu.';
 }catch(e){$('saved-profile-status').textContent='Nie zapisano profilu: '+e.message;}
 finally{button.disabled=false;}
};
$('delete-port-profile').onclick=async()=>{
 const p=selectedSavedPortProfile();if(!p){alert('Wybierz profil do usunięcia.');return;}
 if(!confirm('Usunąć zapisany profil „'+p.name+'”? Nie zmieni to konfiguracji switcha.'))return;
 const button=$('delete-port-profile');button.disabled=true;
 try{
  const result=await api('/api/port-profiles?id='+encodeURIComponent(p.id),undefined,'DELETE');
  savedPortProfiles=Array.isArray(result.profiles)?result.profiles:[];
  renderSavedPortProfiles();renderComplianceProfiles();$('saved-profile-status').textContent='Profil „'+p.name+'” został usunięty. Konfiguracja switcha pozostała bez zmian.';
 }catch(e){$('saved-profile-status').textContent='Nie udało się usunąć profilu: '+e.message;}
 finally{button.disabled=false;}
};
refreshSavedPortProfiles();


function detectPortRole(port){
 const description=String(port.description||'').trim();
 const value=description.toLocaleLowerCase('pl-PL');
 const rules=[
  {role:'Uplink / switch',profile:'Trunk uplink',pattern:/\b(uplink|up-link|switch|router|core|dist|agregacj|lacp|po[0-9]+)\b/},
  {role:'Access Point',profile:'Access Point',pattern:/\b(ap|access.?point|wifi|wi-fi|wlan)\b/},
  {role:'CCTV / kamera',profile:'CCTV',pattern:/\b(cctv|kamera|camera|nvr|rejestrator)\b/},
  {role:'VoIP / telefon',profile:'VoIP',pattern:/\b(voip|telefon|phone|sip)\b/},
  {role:'Serwer / storage',profile:'Server',pattern:/\b(serwer|server|nas|storage|hypervisor|esxi|proxmox|idrac|ilo)\b/},
  {role:'Stacja robocza',profile:'Access standard',pattern:/\b(pc|komputer|workstation|desktop|laptop|drukarka|printer)\b/}
 ];
 const match=rules.find(rule=>rule.pattern.test(value));
 return match?{role:match.role,profile:match.profile,source:'Opis: '+description,confidence:'jawna'}:{role:'Nieustalona',profile:'Brak rekomendacji',source:description?'Opis nie zawiera rozpoznawalnego oznaczenia: '+description:'Brak opisu portu',confidence:'brak'};
}
function renderPortRoleAudit(){
 const rows=latestPorts.map(port=>({port:port.name||'—',...detectPortRole(port)}));
 const recognized=rows.filter(row=>row.confidence==='jawna').length;
 $('port-role-count').textContent=recognized+' / '+rows.length+' rozpoznanych na podstawie opisu';
 $('port-role-rows').innerHTML=rows.length?rows.map(row=>'<tr><td><b>'+esc(row.port)+'</b></td><td>'+esc(row.role)+'</td><td>'+esc(row.profile)+'</td><td>'+esc(row.source)+'</td></tr>').join(''):'<tr><td colspan="4" class="empty">Brak portów do analizy.</td></tr>';
}
function runPortAudit(){
 const rows=[];
 for(const p of latestPorts){
  const port=p.name||'—',admin=String(p.admin||'unknown').toLowerCase(),oper=String(p.oper||'unknown').toLowerCase();
  const description=String(p.description||'').trim(),speed=String(p.speed||'').trim();
  if(admin==='up'&&oper==='down')rows.push({level:'warning',port,title:'Port włączony administracyjnie, ale bez linku',value:'admin up · oper down',advice:'Sprawdź kabel, urządzenie końcowe i negocjację łącza. Jeśli port jest nieużywany, rozważ osobną politykę wyłączania.'});
  if(!description)rows.push({level:'info',port,title:'Brak opisu portu',value:'description pusty',advice:'Dodaj opis wskazujący urządzenie, lokalizację lub przeznaczenie portu.'});
  if(oper==='up'&&!speed)rows.push({level:'info',port,title:'Brak odczytu prędkości aktywnego łącza',value:'oper up · speed brak danych',advice:'Ponów odczyt SNMP i sprawdź obsługę IF-MIB dla tego interfejsu.'});
  if(admin==='down'&&oper==='up')rows.push({level:'warning',port,title:'Niespójny stan administracyjny i operacyjny',value:'admin down · oper up',advice:'Zweryfikuj aktualność odczytu SNMP oraz stan interfejsu na switchu.'});
 }
 const warnings=rows.filter(x=>x.level==='warning').length,infos=rows.filter(x=>x.level==='info').length;
 $('port-audit-summary').innerHTML='<div class="metric"><span>Porty sprawdzone</span><b>'+latestPorts.length+'</b></div><div class="metric"><span>Wymagają uwagi</span><b>'+warnings+'</b></div><div class="metric"><span>Informacyjne</span><b>'+infos+'</b></div>';
 $('port-audit-status').textContent='Audyt zakończony na podstawie '+latestPorts.length+' portów z ostatniego odczytu SNMP. '+(rows.length?'Wykryto '+rows.length+' wskazówek do weryfikacji.':'Nie wykryto wskazanych niezgodności.')+' Wyniki nie oznaczają automatycznie awarii.';
 $('port-audit-rows').innerHTML=rows.length?rows.map(x=>'<tr><td><span class="state '+(x.level==='warning'?'down':'unknown')+'">'+(x.level==='warning'?'Uwaga':'Info')+'</span></td><td><b>'+esc(x.port)+'</b></td><td>'+esc(x.title)+'</td><td>'+esc(x.value)+'</td><td>'+esc(x.advice)+'</td></tr>').join(''):'<tr><td colspan="5" class="empty">Nie wykryto problemów w kontrolowanych kryteriach.</td></tr>';
 renderPortRoleAudit();
}


$('refresh-topology-map').onclick=async()=>{
 const button=$('refresh-topology-map'),status=$('topology-map-status'),host=$('topology-map-content');
 button.disabled=true;button.textContent='Odczytuję…';status.textContent='Pobieranie konfiguracji, switchport, LLDP i LACP…';host.innerHTML='<div class="empty">Analiza topologii…</div>';
 try{
  const data=await api('/api/topology/map');
  if(!data.available||!Array.isArray(data.ports)||!Array.isArray(data.links)||!Array.isArray(data.aggregations)||!Array.isArray(data.risks))throw new Error('Niekompletna odpowiedź mapy topologii.');
  const checks=data.checks||{},portCount=data.ports.length,linkCount=data.links.length,lagCount=data.aggregations.length,risks=data.risks;
  const critical=risks.filter(x=>x.severity==='critical').length,warning=risks.filter(x=>x.severity==='warning').length,info=risks.filter(x=>x.severity==='info').length;
  status.textContent='Odczytano '+portCount+' portów · '+linkCount+' rozpoznanych sąsiadów LLDP · '+lagCount+' agregacji · '+warning+' ostrzeżeń · '+info+' informacji. Źródła: running-config '+(checks.runningConfig?'OK':'brak')+', switchport '+(checks.switchport?'OK':'niedostępny')+', LLDP '+(checks.lldp?'OK':'brak rozpoznanych wpisów')+', LACP '+(checks.lacp?'OK':'brak rozpoznanych wpisów')+'.';
  const severityLabel=x=>x.severity==='critical'?'Krytyczne':x.severity==='warning'?'Ostrzeżenie':'Informacja';
  const severityClass=x=>x.severity==='critical'?'down':x.severity==='warning'?'unknown':'';
  const impactByCode=new Map((data.impacts||[]).map(x=>[x.code,x]));
  const riskCards=risks.map(x=>{
   const impact=impactByCode.get(x.code);
   const impactHTML=impact?'<div class="topology-impact"><b>Co może zostać dotknięte</b><span>Porty skonfigurowane: '+esc((impact.configuredPorts||[]).join(' · ')||'Brak rozpoznanych')+'</span>'+(impact.livePorts?.length?'<span>Porty aktywne w odczycie LACP: '+esc(impact.livePorts.join(' · '))+'</span>':'')+'<span>Potencjalnie dotknięte VLAN-y: '+esc(impact.vlanEvidenceAvailable?(impact.vlans||[]).map(v=>'VLAN '+v).join(', ')||'Brak rozpoznanych':'Brak wiarygodnych danych VLAN')+'</span>'+(impact.neighbors?.length?'<span>Sąsiedzi LLDP: '+esc(impact.neighbors.join(' · '))+'</span>':'')+'<small>'+esc(impact.note)+'</small></div>':'';
   return '<article class="topology-risk"><div><span class="state '+severityClass(x)+'">'+severityLabel(x)+'</span><b>'+esc(x.title)+'</b></div><p>'+esc(x.evidence)+'</p><small>'+esc(x.recommendation)+'</small>'+impactHTML+'</article>';
  }).join('');
  const linkCards=data.links.map(x=>'<article class="topology-neighbor"><small>LLDP · '+esc(x.localPort)+'</small><b>'+esc(x.neighbor)+'</b><span>Rozpoznany sąsiad — fizyczna ciągłość i stan portu wymagają potwierdzenia.</span></article>').join('');
  const lagCards=data.aggregations.map(x=>{
   const state=x.ready==='Gotowy'?'up':x.ready==='Niegotowy'?'down':'unknown';
   const live=x.live?'Odczyt LACP aktywny':'Brak potwierdzenia stanu LACP';
   return '<article class="topology-lag"><div><b>'+esc(x.name)+'</b><span class="state '+state+'">'+esc(x.ready||'Status niedostępny')+'</span></div><small>Konfiguracja: '+esc(x.members.join(' · ')||'Brak członków')+'</small><small>Stan operacyjny: '+esc(x.liveMembers?.join(' · ')||live)+'</small>'+(x.memberMismatch?'<small class="topology-warning-text">Członkowie skonfigurowani i aktywni nie są zgodni</small>':'')+(x.partner?'<small>Partner: '+esc(x.partner)+'</small>':'')+'</article>';
  }).join('');
  const portCards=data.ports.map(x=>'<article class="topology-port"><div><b>'+esc(x.port)+'</b><span class="state '+(x.protected?'unknown':x.mode==='trunk'?'unknown':x.mode==='access'?'up':'')+'">'+esc(x.protected?'Chroniony':x.role||x.mode||'Rola nieznana')+'</span></div>'+(x.description?'<small>Opis: '+esc(x.description)+'</small>':'')+'<small>Tryb: '+esc(x.mode||'Nieznany')+' · VLAN: '+esc((x.vlans||[]).join(', ')||'Brak odczytu')+(x.nativeVlan?' · Native/PVID: '+esc(x.nativeVlan):'')+'</small>'+(x.lag?'<small>Agregacja: '+esc(x.lag)+'</small>':'')+(x.edgePort?'<small>EdgePort</small>':'')+'</article>').join('');
  const vlanCards=(data.vlans||[]).map(x=>'<span class="topology-vlan">VLAN '+esc(x)+'</span>').join('');
  host.innerHTML='<div class="topology-summary-grid"><article><small>PORTY</small><b>'+portCount+'</b></article><article><small>SĄSIEDZI LLDP</small><b>'+linkCount+'</b></article><article><small>AGREGACJE LACP</small><b>'+lagCount+'</b></article><article><small>RYZYKA</small><b>'+risks.length+'</b></article></div><div class="topology-section"><h4>Ryzyka redundancji i ciągłości łączności</h4><div class="topology-risk-grid">'+(riskCards||'<div class="empty">Nie wykryto ryzyk w dostępnych, rozpoznanych danych. To nie jest gwarancja redundancji.</div>')+'</div></div><div class="topology-section"><h4>Połączenia wykryte przez LLDP</h4><div class="topology-neighbor-grid">'+(linkCards||'<div class="empty">Brak rozpoznanych sąsiadów. Nie oznacza to braku fizycznych połączeń.</div>')+'</div></div><div class="topology-section"><h4>Agregacje LACP</h4><div class="topology-lag-grid">'+(lagCards||'<div class="empty">Brak rozpoznanych agregacji z running-config.</div>')+'</div></div><div class="topology-section"><h4>Porty lokalnego przełącznika</h4><div class="topology-port-grid">'+(portCards||'<div class="empty">Brak rozpoznanych interfejsów portowych.</div>')+'</div></div><div class="topology-section"><h4>VLAN zadeklarowane</h4><div class="topology-vlan-list">'+(vlanCards||'<span class="note">Brak rozpoznanych VLAN.</span>')+'</div></div><p class="note">'+esc(data.note||'')+'</p>';
  if(!checks.lldp||!checks.switchport)status.textContent+=' Mapa jest częściowa — brakujące źródła nie są uzupełniane domysłami.';
 }catch(error){status.textContent='Nie udało się zbudować mapy: '+error.message;host.innerHTML='<div class="empty">Brak wiarygodnych danych. Poprzednia mapa została wyczyszczona.</div>';}
 finally{button.disabled=false;button.textContent='Odczytaj topologię';}
};
$('run-security-audit').onclick=async()=>{
 const button=$('run-security-audit');button.disabled=true;button.textContent='Analizuję…';
 $('security-audit-summary').textContent='Pobieranie running-config. Nie są wykonywane żadne zmiany.';
 $('security-audit-rows').innerHTML='<tr><td colspan="4" class="empty">Odczyt konfiguracji…</td></tr>';
 try{
  const result=await api('/api/security-audit');
  if(!result.available||!Array.isArray(result.findings))throw new Error('Odpowiedź audytu jest niekompletna.');
  const findings=result.findings;
  const critical=findings.filter(x=>x.severity==='critical').length,warning=findings.filter(x=>x.severity==='warning').length,info=findings.filter(x=>x.severity==='info').length;
  $('security-audit-summary').textContent='Audyt zakończony · '+findings.length+' wykryć · '+critical+' krytycznych · '+warning+' ostrzeżeń · '+info+' do weryfikacji. '+(findings.length?'Wymagana ręczna weryfikacja przed zmianami.':'Nie wykryto jawnych wpisów z kontrolowanej listy. To nie oznacza pełnego bezpieczeństwa.');
  const severityLabel=x=>x.severity==='critical'?'Krytyczne':x.severity==='warning'?'Ostrzeżenie':'Do weryfikacji';
  const severityClass=x=>x.severity==='critical'?'down':x.severity==='warning'?'unknown':'';
  $('security-audit-rows').innerHTML=findings.length?findings.map(x=>'<tr><td><span class="state '+severityClass(x)+'">'+severityLabel(x)+'</span></td><td><b>'+esc(x.title)+'</b></td><td>'+esc(x.evidence)+'</td><td>'+esc(x.recommendation)+'</td></tr>').join(''):'<tr><td colspan="4" class="empty">Brak jawnych wpisów wskazujących na kontrolowane ryzyka. Nie jest to pełny audyt bezpieczeństwa.</td></tr>';
 }catch(error){
  $('security-audit-summary').textContent='Audyt przerwany: '+error.message;
  $('security-audit-rows').innerHTML='<tr><td colspan="4" class="empty">Brak wiarygodnych danych — nie wyciągnięto wniosków.</td></tr>';
 }finally{button.disabled=false;button.textContent='Sprawdź bezpieczeństwo';}
};
$('run-port-audit').onclick=async()=>{
 const button=$('run-port-audit');button.disabled=true;button.textContent='Odświeżam dane…';
 $('port-audit-status').textContent='Pobieranie świeżego odczytu SNMP…';
 try{
  const d=await api('/api/status');
  if(!d.reachable)throw new Error('Switch jest nieosiągalny — wynik nie może być uznany za aktualny.');
  if(!Array.isArray(d.ports)||!d.ports.length)throw new Error('Odczyt nie zawiera listy portów.');
  const freshPorts=sorted(d.ports);
  if(!freshPorts.every(p=>p&&p.name))throw new Error('Odczyt portów jest niekompletny.');
  latestPorts=freshPorts;
  $('port-audit-updated').textContent='Odczyt: '+new Date().toLocaleTimeString();
  runPortAudit();
 }catch(e){
  $('port-audit-summary').innerHTML='<div class="metric"><span>Porty sprawdzone</span><b>—</b></div><div class="metric"><span>Wymagają uwagi</span><b>—</b></div><div class="metric"><span>Informacyjne</span><b>—</b></div>';
  $('port-audit-rows').innerHTML='<tr><td colspan="5" class="empty">Brak aktualnych danych — audyt nie został wykonany.</td></tr>';
  $('port-role-count').textContent='Brak aktualnego odczytu';
  $('port-role-rows').innerHTML='<tr><td colspan="4" class="empty">Nie można rozpoznać ról bez aktualnych danych portów.</td></tr>';
  $('port-audit-status').textContent='Audyt przerwany: '+e.message;
 }finally{button.disabled=false;button.textContent='Uruchom audyt';}
};
function compareValue(value){return value===undefined||value===null||value===''?'Brak danych':String(value);}
function comparePortRows(a,b){
 const fields=[
  ['Tryb portu',a.config?.mode,b.config?.mode],
  ['Access / Native VLAN',a.config?.defaultVlan,b.config?.defaultVlan],
  ['Dozwolone VLAN',Array.isArray(a.config?.configuredVlans)?a.config.configuredVlans.join(', '):undefined,Array.isArray(b.config?.configuredVlans)?b.config.configuredVlans.join(', '):undefined],
  ['Administracyjnie',a.status?.admin,b.status?.admin],
  ['Stan linku',a.status?.oper,b.status?.oper],
  ['Prędkość',a.status?.speed,b.status?.speed],
  ['Duplex',a.status?.duplex,b.status?.duplex],
  ['Opis',a.status?.description,b.status?.description]
 ];
 const body=$('compare-rows');
 body.innerHTML=fields.map(([label,av,bv])=>{
  const left=compareValue(av),right=compareValue(bv);
  const known=left!=='Brak danych'&&right!=='Brak danych';
  const same=known&&left.toLowerCase()===right.toLowerCase();
  const result=!known?'<span class="state unknown">Brak danych</span>':same?'<span class="state up">Zgodne</span>':'<span class="state down">Różnica</span>';
  return '<tr><td><b>'+esc(label)+'</b></td><td>'+esc(left)+'</td><td>'+esc(right)+'</td><td>'+result+'</td></tr>';
 }).join('');
 const differences=fields.filter(([,av,bv])=>av!==undefined&&bv!==undefined&&String(av).toLowerCase()!==String(bv).toLowerCase()).length;
 const missing=fields.filter(([,av,bv])=>av===undefined||bv===undefined||av===null||bv===null||av===''||bv==='').length;
 $('compare-summary').textContent='Porównanie zakończone: '+differences+' wykrytych różnic · '+missing+' parametrów bez kompletu danych. Brak danych nie jest traktowany jako zgodność.';
}
$('compare-ports').onclick=async()=>{
 const portA=$('compare-port-a').value,portB=$('compare-port-b').value,button=$('compare-ports');
 if(!portA||!portB){alert('Wybierz oba porty.');return;}
 if(portA===portB){alert('Wybierz dwa różne porty.');return;}
 button.disabled=true;$('compare-summary').textContent='Odczytuję konfiguracje '+portA+' i '+portB+'…';
 $('compare-rows').innerHTML='<tr><td colspan="4" class="empty">Pobieranie danych ze switcha…</td></tr>';
 try{
  const [ca,cb]=await Promise.all([
   api('/api/port/switchport?port='+encodeURIComponent(portA)),
   api('/api/port/switchport?port='+encodeURIComponent(portB))
  ]);
  const sa=latestPorts.find(p=>p.name===portA)||{},sb=latestPorts.find(p=>p.name===portB)||{};
  comparePortRows({config:ca,status:sa},{config:cb,status:sb});
 }catch(e){
  $('compare-summary').textContent='Nie udało się odczytać obu konfiguracji: '+e.message;
  $('compare-rows').innerHTML='<tr><td colspan="4" class="empty">Porównanie niekompletne. Sprawdź dostępność odczytu switchport.</td></tr>';
 }
 finally{button.disabled=false;}
};

function renderComplianceProfiles(){
 const select=$('compliance-profile'),selected=select.value;
 select.innerHTML='<option value="">Wybierz profil…</option>'+savedPortProfiles.map(p=>'<option value="'+esc(p.id)+'">'+esc(p.name)+' · '+esc(p.mode.toUpperCase())+'</option>').join('');
 if(savedPortProfiles.some(p=>p.id===selected))select.value=selected;
}
function parseProfileVLANs(raw){
 const values=new Set();
 for(const token of String(raw||'').split(',')){
  const part=token.trim();if(!part)continue;
  const match=part.match(/^(\d{1,4})(?:-(\d{1,4}))?$/);
  if(!match)return null;
  const first=Number(match[1]),last=Number(match[2]||match[1]);
  if(first<1||last>4094||last<first||last-first>4093)return null;
  for(let vlan=first;vlan<=last;vlan++)values.add(vlan);
 }
 return [...values].sort((a,b)=>a-b);
}
function complianceCell(label,expected,actual,normalizer){
 const knownExpected=expected!==undefined&&expected!==null&&expected!=='';
 const knownActual=actual!==undefined&&actual!==null&&actual!=='';
 if(!knownExpected||!knownActual)return {label,expected:compareValue(expected),actual:compareValue(actual),state:'unknown'};
 const norm=normalizer|| (v=>String(v).trim().toLocaleLowerCase('pl-PL'));
 const same=norm(expected)===norm(actual);
 return {label,expected:String(expected),actual:String(actual),state:same?'ok':'diff'};
}
let lastComplianceRepairs=[];
function complianceRepairRole(port){
 const status=latestPorts.find(p=>p.name===port)||{};
 const description=String(status.description||'').toLocaleLowerCase('pl-PL');
 const uplink=/\b(uplink|up-link|switch|router|core|dist|agregacj|lacp|po[0-9]+|management|mgmt)\b|zarz[aą]d/.test(description);
 const role=detectPortRole(status);
 return {status,role,uplink};
}
function renderComplianceRecommendations(profile,repairs){
 const host=$('compliance-recommendations');
 if(!host)return;
 if(!repairs.length){
  host.innerHTML='<div class="empty">Nie wykryto różnic, dla których można bezpiecznie przygotować rekomendację.</div>';
  return;
 }
 host.innerHTML='<div class="repair-intro"><b>Proponowane działania</b><span>Każda pozycja jedynie wypełnia formularz. Zmiana nie jest wysyłana automatycznie.</span></div><div class="table-wrap"><table><thead><tr><th>PORT</th><th>WYKRYTE RÓŻNICE</th><th>OCENA</th><th>AKCJA</th></tr></thead><tbody>'+repairs.map(item=>{
  const status=item.blocked?'<span class="state down">Zablokowana</span>':item.patchBlocked||item.risk?'<span class="state unknown">Wymaga uwagi</span>':'<span class="state up">Do przygotowania</span>';
  return '<tr><td><b>'+esc(item.port)+'</b></td><td>'+esc(item.changes.join(' · '))+'</td><td>'+status+(item.reason?'<small class="repair-reason">'+esc(item.reason)+'</small>':'')+'</td><td><div class="repair-actions"><button type="button" class="btn primary repair-patch-btn" data-repair-port="'+esc(item.port)+'" '+(item.patchBlocked?'disabled':'')+'>Napraw tylko różnice</button><button type="button" class="btn ghost repair-profile-btn" data-repair-port="'+esc(item.port)+'" '+(item.blocked?'disabled':'')+'>Pełny profil</button></div></td></tr>';
 }).join('')+'</tbody></table></div><p class="note">Formularz profilu masowego jest pełnym profilem: może również ustawić speed/duplex zgodnie z zapisanym profilem. Te wartości nie są wiarygodnie odczytywane w audycie. Sprawdź je przed zatwierdzeniem. Podgląd i backend nadal blokują porty z PROTECTED_PORTS; przed zapisem wykonywana jest kopia konfiguracji.</p>';
 host.querySelectorAll('.repair-patch-btn:not(:disabled)').forEach(button=>button.addEventListener('click',async()=>{
  const repair=repairs.find(item=>item.port===button.dataset.repairPort);
  if(!repair||!repair.patch)return;
  const payload=repair.patch;
  button.disabled=true;button.textContent='Sprawdzam…';
  try{
   const preview=await api('/api/ports/repair/preview',payload);
   const summary='Naprawa częściowa portu '+repair.port+'\n\nWykryte różnice: '+repair.changes.join(', ')+'\n\nZostaną zmienione wyłącznie wskazane parametry. Tryb portu, speed i duplex pozostaną bez zmian.\n\nBieżący tryb: '+(preview.current?.mode||'—')+' · VLAN: '+(preview.current?.defaultVlan||'—')+(Array.isArray(preview.warnings)&&preview.warnings.length?'\n\nAnaliza wpływu na łączność:\n'+preview.warnings.map(x=>'• '+x).join('\n'):'')+'\n\nPrzed zmianą zostanie wykonana kopia running-config. Kontynuować?';
   if(!confirm(summary))return;
   payload.acknowledgeConnectivityRisk=true;
   button.textContent='Zapisuję…';
   await api('/api/ports/repair/apply',payload);
   let actual=await api('/api/port/switchport?port='+encodeURIComponent(repair.port));
   const checks=[];
   if(payload.updateAccessVLAN)checks.push(Number.parseInt(actual.defaultVlan,10)===payload.accessVlan);
   if(payload.updateTrunk){
    const expected=parseVlanSelection(payload.allowed)||[];
    const got=Array.isArray(actual.configuredVlans)?actual.configuredVlans.map(Number).sort((a,b)=>a-b):[];
    checks.push(Number.parseInt(actual.defaultVlan,10)===payload.native&&expected.length===got.length&&expected.every((v,i)=>v===got[i]));
   }
   if(payload.updateDescription){
    const fresh=await api('/api/status');const port=(fresh.ports||[]).find(p=>p.name===repair.port);
    checks.push(!!port&&String(port.description||'')===payload.description);
   }
   const verified=checks.length>0&&checks.every(Boolean);
   showFriendlyResult('bulk-output',(verified?'Potwierdzono naprawę częściową portu ':'Zmiana wysłana, ale odczyt zwrotny nie potwierdził wszystkich parametrów: ')+repair.port+'. '+(verified?'Wszystkie wybrane parametry zgadzają się z profilem.':'Sprawdź stan portu przed kolejnymi zmianami.'),'Naprawa częściowa portu');
   addEvent((verified?'Potwierdzono':'Niepotwierdzona naprawa częściowa')+' portu '+repair.port,verified?'success':'warning');
   await load();
   if(!verified)alert('Zmiana została wysłana, ale odczyt zwrotny nie potwierdził wszystkich parametrów. Sprawdź port.');
   else alert('Naprawa częściowa zakończona i potwierdzona odczytem zwrotnym.');
  }catch(error){showFriendlyResult('bulk-output',error.message,'Naprawa częściowa portu');alert(error.message);}
  finally{button.disabled=false;button.textContent='Napraw tylko różnice';}
 }));
 host.querySelectorAll('.repair-profile-btn:not(:disabled)').forEach(button=>button.addEventListener('click',()=>{
  const repair=repairs.find(item=>item.port===button.dataset.repairPort);
  if(!repair)return;
  const match=repair.port.match(/^port1\.0\.(\d+)$/i);
  if(!match||Number(match[1])<1||Number(match[1])>52){alert('Nie można bezpiecznie odwzorować nazwy portu na formularz operacji masowej.');return;}
  $('bulk-ports').value=match[1];
  $('bulk-mode').value=profile.mode;
  $('bulk-profile').value='';
  $('bulk-access-vlan').value=String(profile.accessVlan||1);
  $('bulk-allowed').value=profile.allowed||'';
  $('bulk-native').value=String(profile.native||1);
  $('bulk-speed').value=profile.speed||'auto';
  $('bulk-duplex').value=profile.duplex||'auto';
  $('bulk-description').value=repair.description;
  $('bulk-mode').onchange();
  if(profile.mode==='trunk'){$('bulk-native').disabled=false;$('bulk-allowed').disabled=false;}
  else{$('bulk-access-vlan').disabled=false;$('bulk-allowed').disabled=true;$('bulk-native').disabled=true;}
  $('bulk-output').textContent='Przygotowano port '+repair.port+' z profilu „'+profile.name+'”. Wykryte różnice: '+repair.changes.join(', ')+'. Sprawdź wszystkie pola, w szczególności speed/duplex. Następnie użyj „Sprawdź i zastosuj do portów” — wymagany będzie osobny podgląd i potwierdzenie.';
  $('bulk-port-settings').scrollIntoView({behavior:'smooth',block:'start'});
 }));
}
function renderComplianceResults(profile,items,statusByPort){
 const rows=[];let diff=0,unknown=0,ok=0;
 const repairs=[];
 const mode=String(profile.mode||'').toLowerCase();
 const expectedList=mode==='access'?[Number(profile.accessVlan)]:parseProfileVLANs(profile.allowed);
 for(const item of items){
  const status=statusByPort.get(item.port)||{};
  const portRows=[];
  const add=(field)=>{rows.push({port:item.port,field});if(field.state==='diff')portRows.push(field.label);};
  const modeField=complianceCell('Tryb',mode,item.mode);add(modeField);
  const expectedVlan=mode==='access'?profile.accessVlan:profile.native;
  const vlanField=complianceCell(mode==='access'?'Access VLAN':'Native VLAN',expectedVlan,item.defaultVlan);add(vlanField);
  const actualList=Array.isArray(item.configuredVlans)?[...item.configuredVlans].map(Number).sort((a,b)=>a-b):null;
  const listField=complianceCell('Dozwolone VLAN',expectedList?expectedList.join(', '):undefined,actualList?actualList.join(', '):undefined,v=>String(v).split(',').map(x=>x.trim()).filter(Boolean).sort((a,b)=>Number(a)-Number(b)).join(','));
  add(listField);
  if(profile.portDescription!==undefined&&profile.portDescription!==null&&String(profile.portDescription).trim()!==''){
   add(complianceCell('Opis portu',profile.portDescription,status.description));
  }
  if(profile.speed&&profile.speed!=='auto')rows.push({port:item.port,field:{label:'Prędkość skonfigurowana',expected:profile.speed+' Mb/s',actual:'Brak wiarygodnego odczytu konfiguracji',state:'unknown'}});
  if(profile.duplex&&profile.duplex!=='auto')rows.push({port:item.port,field:{label:'Duplex skonfigurowany',expected:profile.duplex,actual:'Brak wiarygodnego odczytu konfiguracji',state:'unknown'}});
  if(portRows.length){
   const role=complianceRepairRole(item.port);
   const profileAccess=mode==='access';
   const descriptionUnknown=(role.status.description===undefined||role.status.description===null)&&String(profile.portDescription||'').trim()!=='';
   const blocked=(role.uplink&&profileAccess)||descriptionUnknown;
   const portMatch=item.port.match(/^port1\.0\.(\d+)$/i);
   const unsupported=!portMatch||Number(portMatch[1])<1||Number(portMatch[1])>52;
   const modeMismatch=!String(item.mode||'').toLowerCase()||String(item.mode).toLowerCase()!==mode;
   const patch={port:item.port,mode:String(item.mode||'').toLowerCase(),accessVlan:Number(profile.accessVlan),allowed:profile.allowed||'',native:Number(profile.native),description:String(profile.portDescription||''),updateAccessVLAN:mode==='access'&&portRows.includes('Access VLAN'),updateTrunk:mode==='trunk'&&(portRows.includes('Native VLAN')||portRows.includes('Dozwolone VLAN')),updateDescription:portRows.includes('Opis portu')};
   const patchBlocked=blocked||unsupported||modeMismatch||(!patch.updateAccessVLAN&&!patch.updateTrunk&&!patch.updateDescription);
   repairs.push({port:item.port,changes:portRows,patch,patchBlocked,description:String(profile.portDescription||'').trim()!==''?profile.portDescription:String(role.status.description||''),blocked:blocked||unsupported,risk:!blocked&&!unsupported,reason:role.uplink&&profileAccess?'Opis portu wskazuje uplink, switch, LACP lub zarządzanie — rekomendacja profilu Access jest zablokowana.':descriptionUnknown?'Nie odczytano opisu portu, a profil ma go zmienić — blokada, aby nie nadpisać nieznanej wartości.':unsupported?'Nazwa portu nie pasuje do zakresu formularza operacji masowej (1–52).':modeMismatch?'Różnica trybu wymaga pełnej, osobno potwierdzonej zmiany profilu.':'Pełny profil może zmienić speed/duplex; naprawa częściowa pozostawia je bez zmian.'});
  }
 }
 const badge=state=>state==='ok'?'<span class="state up">Zgodne</span>':state==='diff'?'<span class="state down">Różnica</span>':'<span class="state unknown">Brak danych</span>';
 $('compliance-rows').innerHTML=rows.map(({port,field})=>{
  if(field.state==='ok')ok++;else if(field.state==='diff')diff++;else unknown++;
  return '<tr><td><b>'+esc(port)+'</b></td><td>'+esc(field.label)+'</td><td>'+esc(field.expected)+'</td><td>'+esc(field.actual)+'</td><td>'+badge(field.state)+'</td></tr>';
 }).join('')||'<tr><td colspan="5" class="empty">Brak wyników do wyświetlenia.</td></tr>';
 lastComplianceRepairs=repairs;
 renderComplianceRecommendations(profile,repairs);
 $('compliance-summary').textContent='Profil „'+profile.name+'”: '+diff+' różnic · '+unknown+' parametrów bez danych · '+ok+' zgodnych odczytów. Rekomendacje obejmują tylko jawnie wykryte różnice; brak danych nie jest traktowany jako niezgodność.';
}
$('run-compliance').onclick=async()=>{
 const selected=[...$('compliance-ports').selectedOptions].map(o=>o.value);
 const profile=savedPortProfiles.find(p=>p.id===$('compliance-profile').value);
 const button=$('run-compliance');
 if(!selected.length){alert('Wybierz co najmniej jeden port.');return;}
 if(!profile){alert('Wybierz zapisany profil.');return;}
 button.disabled=true;lastComplianceRepairs=[];$('compliance-recommendations').innerHTML='<div class="empty">Analiza trwa…</div>';
 $('compliance-summary').textContent='Odczytuję aktualny stan SNMP i konfigurację switchport…';
 $('compliance-rows').innerHTML='<tr><td colspan="5" class="empty">Trwa jeden zbiorczy odczyt konfiguracji…</td></tr>';
 try{
  const [statusData,configData]=await Promise.all([
   api('/api/status'),
   api('/api/ports/switchport/bulk',{ports:selected})
  ]);
  if(!statusData.reachable)throw new Error('Switch jest nieosiągalny — wynik nie może być uznany za aktualny.');
  latestPorts=sorted(Array.isArray(statusData.ports)?statusData.ports:[]);
  const statusByPort=new Map((Array.isArray(statusData.ports)?statusData.ports:[]).map(p=>[p.name,p]));
  const items=Array.isArray(configData.ports)?configData.ports:[];
  if(items.length!==selected.length)throw new Error('Odczyt konfiguracji nie zawiera wszystkich wybranych portów.');
  const byPort=new Map(items.map(p=>[p.port,p]));
  const ordered=selected.map(port=>byPort.get(port));
  if(ordered.some(item=>!item))throw new Error('Brak kompletnego odczytu jednego z portów.');
  renderComplianceResults(profile,ordered,statusByPort);
 }catch(e){
  $('compliance-summary').textContent='Nie można potwierdzić zgodności: '+e.message;
  $('compliance-rows').innerHTML='<tr><td colspan="5" class="empty">Wynik niekompletny — nie wyciągnięto wniosków o zgodności.</td></tr>';lastComplianceRepairs=[];$('compliance-recommendations').innerHTML='<div class="empty">Brak rekomendacji — odczyt niekompletny.</div>';
 }finally{button.disabled=false;}
};
$('bulk-copy-source').onclick=async()=>{
 const port=$('bulk-source-port').value,button=$('bulk-copy-source');
 if(!port){alert('Najpierw wybierz port wzorcowy.');return;}
 button.disabled=true;button.textContent='Odczytuję…';
 try{
  const details=await api('/api/port/switchport?port='+encodeURIComponent(port));
  const mode=String(details.mode||'').toLowerCase(),native=Number.parseInt(details.defaultVlan,10);
  if(!['access','trunk'].includes(mode))throw new Error('Nie rozpoznano trybu Access/Trunk portu wzorcowego.');
  if(!Number.isInteger(native)||native<1||native>4094)throw new Error('Brak wiarygodnego odczytu domyślnego VLAN.');
  if(mode==='trunk'&&(!Array.isArray(details.configuredVlans)||!details.configuredVlans.length))throw new Error('Nie udało się odczytać listy VLAN trunk.');
  $('bulk-mode').value=mode;$('bulk-profile').value='';
  $('bulk-access-vlan').value=String(native);$('bulk-native').value=String(native);
  $('bulk-allowed').value=mode==='trunk'?details.configuredVlans.join(','):'';
  $('bulk-access-vlan').disabled=mode==='trunk';$('bulk-allowed').disabled=mode!=='trunk';$('bulk-native').disabled=mode!=='trunk';
  const source=latestPorts.find(p=>p.name===port);
  if(source?.description)$('bulk-description').value=source.description;
  if(source?.speed){const speed=String(source.speed).match(/(10|100|1000)\s*Mb/i);if(speed)$('bulk-speed').value=speed[1];}
  $('bulk-output').textContent='Wczytano ustawienia '+port+': '+mode.toUpperCase()+', VLAN '+native+(mode==='trunk'?', dozwolone: '+details.configuredVlans.join(', '):'')+'. Ustaw listę portów docelowych i sprawdź podgląd przed zastosowaniem.';
 }catch(e){$('bulk-output').textContent='Nie udało się skopiować konfiguracji: '+e.message;}
 finally{button.disabled=false;button.textContent='Wczytaj ustawienia z portu';}
};

function render(){
 const q=$('filter').value.toLowerCase(),st=$('state-filter').value;
 const ps=latestPorts.filter(p=>(p.name||'').toLowerCase().includes(q)&&(st==='all'||p.oper===st));
 $('port-count').textContent=ps.length+' / '+latestPorts.length+' portów';
 $('ports').innerHTML=ps.length?ps.map(p=>{
  const on=p.admin==='up',rate=portRates.get(p.name),capacity=portCapacity(p),util=rate&&capacity?Math.min(100,100*(rate.rx+rate.tx)/capacity):null;
  const errors=counterHistory.filter(x=>x.port===p.name).reduce((n,x)=>n+(Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0)+(Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0),0);
  const link=p.oper==='up'?'Połączony':p.oper==='down'?'Brak linku':'Nieznany';
  const quality=p.oper==='down'&&on?'Do sprawdzenia':errors>0?'Wykryto problemy':'Bez alarmów';
  return '<tr><td><b>'+esc(p.name)+'</b></td><td><span class="state '+(p.oper==='up'?'up':p.oper==='down'?'down':'unknown')+'">'+link+'</span></td><td>'+esc(p.speed||'—')+'</td><td>'+(rate?formatRate(rate.rx+rate.tx):'Zbieranie pomiarów…')+'</td><td><span class="state '+(quality==='Bez alarmów'?'up':'unknown')+'">'+quality+(util===null?'':' · '+util.toFixed(1)+'%')+'</span></td><td><button class="port-action '+(on?'danger':'')+'" data-port="'+esc(p.name)+'" data-action="'+(on?'disable':'enable')+'">'+(on?'Wyłącz port':'Włącz port')+'</button> <button class="port-action" data-config-port="'+esc(p.name)+'">Konfiguruj</button></td></tr>';
 }).join(''):'<tr><td colspan="6" class="empty">Brak portów pasujących do filtra.</td></tr>';
}
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
 const status=p.admin==='up'&&p.oper==='down'?'Brak linku':score<60?'Krytyczny':score<85?'Wymaga uwagi':'Dobry';
 if(status==='Krytyczny'||status==='Brak linku')critical++;else if(status==='Wymaga uwagi')attention++;
 return {...p,c,o,score,status};}).sort((a,b)=>a.score-b.score||b.c.errors-a.c.errors);
 summary.textContent=critical+' krytyczne · '+attention+' wymagają uwagi · '+rows.length+' portów ocenionych';
 body.innerHTML=rows.length?rows.map(p=>'<tr><td><b>'+esc(p.name)+'</b></td><td><span class="quality-score '+(p.score<60?'bad':p.score<85?'warn':'good')+'">'+p.score+'/100</span></td><td><span class="state '+(p.status==='Dobry'?'up':p.status==='Wymaga uwagi'?'unknown':'down')+'">'+p.status+'</span></td><td>'+fmtCount(p.c.errors+p.c.discards)+'</td><td>'+p.o.count+'</td><td>'+esc(p.speed||'—')+'</td></tr>').join(''):'<tr><td colspan="6" class="empty">Oczekiwanie na odczyt portów.</td></tr>';
}
function renderCounterHistory(){
 const rows=$('counter-history-rows'),rank=$('counter-error-rank');if(!rows||!rank)return;
 const records=[...counterHistory].sort((a,b)=>new Date(b.timestamp)-new Date(a.timestamp));
 const errors=records.reduce((sum,x)=>sum+(Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0),0),discards=records.reduce((sum,x)=>sum+(Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0),0);
 $('counter-history-events').textContent=records.length;$('counter-history-errors').textContent=fmtCount(errors);$('counter-history-discards').textContent=fmtCount(discards);
 rows.innerHTML=records.length?records.slice(0,100).map(x=>'<tr><td>'+esc(new Date(x.timestamp).toLocaleString())+'</td><td><b>'+esc(x.port)+'</b></td><td>'+fmtCount((Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0))+'</td><td>'+fmtCount((Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0))+'</td></tr>').join(''):'<tr><td colspan="4" class="empty">Nie wykryto jeszcze problemów z transmisją.</td></tr>';
 const totals=new Map();for(const x of records){const v=totals.get(x.port)||{errors:0,discards:0,events:0,last:x.timestamp};v.errors+=(Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0);v.discards+=(Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0);v.events++;if(new Date(x.timestamp)>new Date(v.last))v.last=x.timestamp;totals.set(x.port,v);}
 const top=[...totals.entries()].sort((a,b)=>(b[1].errors+b[1].discards)-(a[1].errors+a[1].discards)).slice(0,10);
 rank.innerHTML=top.length?top.map(([p,v])=>'<tr><td><b>'+esc(p)+'</b></td><td>'+fmtCount(v.errors)+'</td><td>'+fmtCount(v.discards)+'</td><td>'+v.events+'</td><td>'+esc(new Date(v.last).toLocaleString())+'</td></tr>').join(''):'<tr><td colspan="5" class="empty">Ranking pojawi się po pierwszym wykrytym problemie.</td></tr>';
}

function updateOverviewHealth(){
 const root=$('overview-health');if(!root)return;
 const state=$('overview-health-state'),message=$('overview-health-message'),link=$('overview-health-link'),errors=$('overview-health-errors');
 const unavailable=latestPorts.length===0;
 const disconnected=$('device-state')?.textContent==='OFFLINE'||$('device-state')?.textContent==='BRAK DANYCH';
 const noLink=latestPorts.filter(p=>p.admin==='up'&&p.oper==='down').length;
 const totals=counterHistory.reduce((sum,x)=>sum+(Number(x.errorRxDelta)||0)+(Number(x.errorTxDelta)||0)+(Number(x.discardRxDelta)||0)+(Number(x.discardTxDelta)||0),0);
 let level='good',label='BEZ WYKRYTYCH PROBLEMÓW',detail='Odczyt SNMP działa; w dostępnej historii nie ma przyrostów błędów transmisji.';
 if(unavailable){level='unknown';label='BRAK DANYCH';detail='Nie ma jeszcze kompletnego odczytu interfejsów.';}
 else if(disconnected){level='critical';label='BRAK ŁĄCZNOŚCI';detail='Panel nie może obecnie odczytać switcha przez SNMP.';}
 else if(noLink>0||totals>0){level='warning';label='WYMAGA WERYFIKACJI';detail=(noLink?noLink+' portów administracyjnie włączonych nie ma linku. ':'')+(totals?fmtCount(totals)+' błędów lub discardów zapisanych w historii.':'');}
 root.dataset.state=level;if(state)state.textContent=label;if(message)message.textContent=detail;if(link)link.textContent=String(noLink);if(errors)errors.textContent=fmtCount(totals);
}
function renderEvents(){
 const el=$('monitor-events');if(!el)return;
 const rows=[];
 for(const x of recentEvents)rows.push({...x,source:'system',port:''});
 for(const x of portHistory){
  const at=Date.parse(x.timestamp)||Date.now(),message=(x.port||'Port')+' — '+(x.state==='up'?'łącze przywrócone':'utrata łącza');
  const duplicate=rows.some(r=>r.source==='system'&&r.message.includes(x.port||'')&&/link|łącze/i.test(r.message)&&Math.abs((Date.parse(r.timestamp)||0)-at)<2000);
  if(!duplicate)rows.push({message,type:x.state==='up'?'success':'danger',timestamp:x.timestamp,at:new Date(at).toLocaleString(),source:'port',port:x.port||''});
 }
 for(const x of counterHistory){
  const at=Date.parse(x.timestamp)||Date.now(),parts=[];
  if(Number(x.errorRxDelta)>0)parts.push('RX błędy +'+fmtCount(x.errorRxDelta));
  if(Number(x.errorTxDelta)>0)parts.push('TX błędy +'+fmtCount(x.errorTxDelta));
  if(Number(x.discardRxDelta)>0)parts.push('RX discard +'+fmtCount(x.discardRxDelta));
  if(Number(x.discardTxDelta)>0)parts.push('TX discard +'+fmtCount(x.discardTxDelta));
  if(!parts.length)continue;
  const duplicate=rows.some(r=>r.source==='system'&&r.message.includes(x.port||'')&&/błęd|discard/i.test(r.message)&&Math.abs((Date.parse(r.timestamp)||0)-at)<2000);
  if(!duplicate)rows.push({message:(x.port||'Port')+' — '+parts.join(', '),type:'warning',timestamp:x.timestamp,at:new Date(at).toLocaleString(),source:'counter',port:x.port||''});
 }
 rows.sort((a,b)=>(Date.parse(b.timestamp)||0)-(Date.parse(a.timestamp)||0));
 const counts={danger:0,warning:0,info:0,success:0};
 rows.forEach(x=>{if(Object.prototype.hasOwnProperty.call(counts,x.type))counts[x.type]++;});
 for(const [id,value] of [['alert-danger-count',counts.danger],['alert-warning-count',counts.warning],['alert-info-count',counts.info],['alert-total-count',rows.length]]){const node=$(id);if(node)node.textContent=String(value);}
 const portSelect=$('alert-port-filter');
 if(portSelect){
  const selected=portSelect.value||'all',names=[...new Set([...latestPorts.map(p=>p.name),...portHistory.map(x=>x.port),...counterHistory.map(x=>x.port)].filter(Boolean))].sort(natural);
  const options='<option value="all">Wszystkie porty</option>'+names.map(name=>'<option value="'+esc(name)+'">'+esc(name)+'</option>').join('');
  if(portSelect.dataset.options!==options){portSelect.innerHTML=options;portSelect.dataset.options=options;portSelect.value=names.includes(selected)?selected:'all';}
 }
 const severity=$('alert-filter')?.value||'all',source=$('alert-source-filter')?.value||'all',port=$('alert-port-filter')?.value||'all',query=($('alert-search')?.value||'').trim().toLocaleLowerCase('pl');
 const visible=rows.filter(x=>(severity==='all'||x.type===severity)&&(source==='all'||x.source===source)&&(port==='all'||x.port===port)&&(!query||(x.message+' '+x.port).toLocaleLowerCase('pl').includes(query))).slice(0,200);
 const sourceLabel={system:'SYSTEM',port:'PORT',counter:'LICZNIKI'},severityLabel={danger:'KRYTYCZNE',warning:'OSTRZEŻENIE',success:'PRZYWRÓCONE',info:'INFO'};
 el.innerHTML=visible.length?visible.map(x=>'<div class="event-row is-'+esc(x.type)+'"><i></i><span class="event-copy"><b>'+esc(x.message)+'</b><small>'+esc(sourceLabel[x.source]||'MONITORING')+' · '+esc(severityLabel[x.type]||'INFO')+'</small></span><time>'+esc(x.at||'')+'</time></div>').join(''):'<div class="empty">Brak zdarzeń pasujących do wybranych filtrów.</div>';
 const summary=$('alert-repeat-summary');if(summary)summary.textContent=visible.length+' z '+rows.length+' zdarzeń · najnowsze pierwsze';
}
function receiveMonitorRecord(record){
 if(!record||typeof record!=='object')return;
 const timestamp=record.timestamp||new Date().toISOString(),at=Date.parse(timestamp)||Date.now();
 if(record.kind==='event'){
  const duplicate=recentEvents.some(item=>item.message===record.message&&item.type===record.type&&Math.abs((Date.parse(item.timestamp)||0)-at)<30000);
  if(duplicate)return;
  recentEvents.unshift({message:String(record.message||'Zdarzenie monitoringu'),type:record.type||'info',at:new Date(at).toLocaleString(),timestamp});
  recentEvents=recentEvents.slice(0,200);renderEvents();updateOverviewHealth();return;
 }
 if(record.kind==='port'){
  portHistory.push({port:record.port,state:record.state,timestamp});portHistory=portHistory.slice(-2000);renderPortHistory();renderPortDetail();renderPortQuality();return;
 }
 if(record.kind==='counter'){
  counterHistory.push({...record,timestamp});counterHistory=counterHistory.slice(-2000);renderCounterHistory();renderPortDetail();renderPortQuality();updateOverviewHealth();
 }
}
function connectMonitorStream(){
 const state=$('monitor-stream-state');if(typeof EventSource==='undefined'){if(state)state.textContent='BRAK OBSŁUGI STREAMU';return;}
 const stream=new EventSource('/api/monitor/stream');
 stream.addEventListener('ready',()=>{if(state){state.textContent='NA ŻYWO';state.classList.remove('stream-offline');state.classList.add('stream-online');}restoreMonitorHistory();});
 stream.addEventListener('monitor',event=>{try{receiveMonitorRecord(JSON.parse(event.data));}catch(_e){}});
 stream.onerror=()=>{if(state){state.textContent='PONOWNE ŁĄCZENIE';state.classList.remove('stream-online');state.classList.add('stream-offline');}};
 document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='visible'&&stream.readyState===EventSource.CLOSED){stream.close();connectMonitorStream();}});
}
function persistMonitorRecord(record){fetch('/api/monitor/history',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(record),keepalive:true}).catch(()=>{});}
function addEvent(message,type='info',persist=true){const now=new Date();recentEvents.unshift({message,at:now.toLocaleTimeString(),timestamp:now.toISOString(),type});recentEvents=recentEvents.slice(0,12);renderEvents();if(persist)persistMonitorRecord({kind:'event',message,type});}
async function restoreMonitorHistory(){try{const d=await api('/api/monitor/history');portHistory=d.portEvents||[];counterHistory=d.counterEvents||[];renderPortHistory();renderCounterHistory();renderPortQuality();recentEvents=(d.events||[]).slice(-200).reverse().map(x=>({message:x.message,type:x.type,timestamp:x.timestamp,at:new Date(x.timestamp).toLocaleString()}));trafficHistory=(d.samples||[]).slice(-30).map(x=>({rx:Number(x.rx)||0,tx:Number(x.tx)||0}));
 for(const sample of d.portSamples||[]){const at=Date.parse(sample.timestamp)||Date.now();for(const [name,rate] of Object.entries(sample.ports||{})){const history=portTrafficHistory.get(name)||[];history.push({at,rx:Number(rate.rx)||0,tx:Number(rate.tx)||0});portTrafficHistory.set(name,history.slice(-60));portRates.set(name,{rx:Number(rate.rx)||0,tx:Number(rate.tx)||0});}}
 renderPortDetail();renderPortTraffic();renderEvents();updateOverviewHealth();drawTraffic();$('traffic-samples').textContent=trafficHistory.length;if(trafficHistory.length){const last=trafficHistory[trafficHistory.length-1];$('traffic-rx').textContent=formatRate(last.rx);$('traffic-tx').textContent=formatRate(last.tx);}}catch(e){console.warn('Historia monitoringu niedostępna:',e.message);}}
function recordStatusEvents(d){if(previousReachable!==null&&d.reachable!==previousReachable)addEvent(d.reachable?'Połączenie SNMP zostało przywrócone':'Utracono połączenie SNMP',d.reachable?'success':'danger');if(d.reachable){if(hasPortBaseline){for(const p of latestPorts){const oldState=previousPortStates.get(p.name);if(oldState&&oldState!==p.oper){addEvent(p.name+': link '+oldState.toUpperCase()+' → '+String(p.oper||'unknown').toUpperCase(),p.oper==='up'?'success':'danger');if((oldState==='up'||oldState==='down')&&(p.oper==='up'||p.oper==='down')){persistMonitorRecord({kind:'port',port:p.name,state:p.oper});portHistory.push({port:p.name,state:p.oper,timestamp:new Date().toISOString()});renderPortHistory();renderPortDetail();}}const old=previousErrorCounters.get(p.name);if(old){const inErrors=Number(p.inErrors||0),outErrors=Number(p.outErrors||0),inDiscards=Number(p.inDiscards||0),outDiscards=Number(p.outDiscards||0),errorRXDelta=inErrors-old.inErrors,errorTXDelta=outErrors-old.outErrors,discardRXDelta=inDiscards-old.inDiscards,discardTXDelta=outDiscards-old.outDiscards;if(errorRXDelta>0||errorTXDelta>0||discardRXDelta>0||discardTXDelta>0){const parts=[];if(errorRXDelta>0)parts.push('RX +'+errorRXDelta+' błędów');if(errorTXDelta>0)parts.push('TX +'+errorTXDelta+' błędów');if(discardRXDelta>0)parts.push('RX +'+discardRXDelta+' discard');if(discardTXDelta>0)parts.push('TX +'+discardTXDelta+' discard');addEvent(p.name+': '+parts.join(', '),'warning');const rec={kind:'counter',port:p.name,errorRxDelta:Math.max(0,errorRXDelta),errorTxDelta:Math.max(0,errorTXDelta),discardRxDelta:Math.max(0,discardRXDelta),discardTxDelta:Math.max(0,discardTXDelta),errorRxTotal:inErrors,errorTxTotal:outErrors,discardRxTotal:inDiscards,discardTxTotal:outDiscards};persistMonitorRecord(rec);counterHistory.push({...rec,timestamp:new Date().toISOString()});counterHistory=counterHistory.slice(-2000);renderCounterHistory();renderPortDetail();}}}}previousPortStates=new Map(latestPorts.map(p=>[p.name,p.oper]));previousErrorCounters=new Map(latestPorts.map(p=>[p.name,{inErrors:Number(p.inErrors||0),outErrors:Number(p.outErrors||0),inDiscards:Number(p.inDiscards||0),outDiscards:Number(p.outDiscards||0)}]));hasPortBaseline=true;}previousReachable=d.reachable;}
function renderMap(){
  const el=$('port-map');if(!el)return;
  const counts={up:0,down:0,unknown:0};
  latestPorts.forEach(p=>{const state=['up','down'].includes(p.oper)?p.oper:'unknown';counts[state]++;});
  const summary=$('port-map-summary');
  if(summary)summary.textContent=latestPorts.length+' portów wykrytych · '+counts.up+' aktywnych · '+counts.down+' nieaktywnych'+(counts.unknown?' · '+counts.unknown+' bez danych':'');
  const filter=$('port-map-filter')?.value||'all';
  const visible=latestPorts.filter(p=>filter==='all'||(p.oper||'unknown')===filter);
  const banks=[
   {id:'copper-a',title:'PORTY 1–24 · 1 Gb/s',test:n=>n>=1&&n<=24,cols:24},
   {id:'copper-b',title:'PORTY 25–48 · 1 Gb/s',test:n=>n>=25&&n<=48,cols:24},
   {id:'combo',title:'PORTY COMBO 49–50',test:n=>n>=49&&n<=50,cols:2},
   {id:'sfp',title:'PORTY SFP+ 51–52 · 10 Gb/s',test:n=>n>=51&&n<=52,cols:2},
   {id:'other',title:'POZOSTAŁE INTERFEJSY',test:n=>!Number.isFinite(n)||n<1||n>52,cols:12}
  ];
  const numberOf=p=>{const match=String(p.name||'').match(/(\d+)$/);const n=match?Number(match[1]):Number(p.index);return Number.isFinite(n)?n:NaN;};
  const renderPort=p=>{
   const state=['up','down'].includes(p.oper)?p.oper:'unknown';
   const label=(p.name||String(p.index)).replace(/^port1\.0\./,'');
   const speed=p.speed||'';
   const speedLabel=(()=>{const value=String(speed).toLowerCase();if(/10000|10g/.test(value))return '10G';if(/2500|2\.5g/.test(value))return '2.5G';if(/1000|1g/.test(value))return '1G';if(/100|100m/.test(value))return '100M';if(/10|10m/.test(value))return '10M';return speed?'—':'—';})();
   const admin=p.admin==='up'?'Włączony':p.admin==='down'?'Wyłączony':'Nieznany';
   return '<button type="button" class="map-port '+(state==='up'?'is-up':state==='down'?'is-down':'is-unknown')+'" data-map-port="'+esc(p.name)+'" aria-label="'+esc(p.name)+', link '+esc(state==='up'?'Połączony':state==='down'?'Brak linku':'Nieznany')+', administracyjnie '+esc(admin)+', '+esc(speed||'prędkość nieznana')+'" title="'+esc(p.name)+' · '+(state==='up'?'Połączony':state==='down'?'Brak linku':'Stan nieznany')+' · port administracyjnie '+esc(admin)+' · '+esc(speed||'Prędkość nieznana')+'"><b>'+esc(label)+'</b><small>'+speedLabel+'</small></button>';
  };
  el.innerHTML=banks.map(bank=>{
   const ports=visible.filter(p=>bank.test(numberOf(p))).sort((a,b)=>numberOf(a)-numberOf(b));
   if(!ports.length)return '';
   return '<section class="port-bank port-bank-'+bank.id+'"><h3 class="port-bank-title">'+bank.title+'</h3><div class="port-bank-grid">'+ports.map(renderPort).join('')+'</div></section>';
  }).join('')||'<div class="empty">Brak portów dla wybranego filtra.</div>';
 }
function fmtCount(v){const n=Number(v||0);return Number.isFinite(n)?new Intl.NumberFormat('pl-PL',{notation:n>999999?'compact':'standard',maximumFractionDigits:1}).format(n):'—';}
function portContextLines(output,port){
 const lines=String(output||'').split(/\r?\n/),tokens=[port.toLowerCase(),port.toLowerCase().replace(/^port/,'')].filter(Boolean);
 const matches=line=>{const value=line.toLowerCase();return tokens.some(token=>{let from=0,index;while((index=value.indexOf(token,from))>=0){const after=value[index+token.length]||'';if(!/[0-9]/.test(after))return true;from=index+1;}return false;});};
 return lines.filter(matches).slice(0,40);
}
function renderPortNetworkContext(){
 const port=$('detail-port')?.value,ctx=portNetworkContext.get(port);
 for(const [id,key,label] of [['detail-vlan-context','vlan','Konfiguracja VLAN portu'],['detail-mac-context','mac','Urządzenia wykryte na porcie'],['detail-lldp-context','lldp','Sąsiedzi sieciowi']]){
  const el=$(id);if(!el)continue;
  if(!port){showFriendlyResult(id,'','Wybierz port');continue;}
  if(!ctx){showFriendlyResult(id,'','Oczekiwanie na odczyt');continue;}
  const result=ctx[key];if(result.error){showFriendlyResult(id,result.error,label);continue;}
  const output=String(result.output||'');
  if(key==='vlan'){
   const parts=output.split('\n\n--- show vlan all ---\n'),switchport=parts[0]||'',vlanLines=portContextLines(parts[1]||'',port);
   const mode=(switchport.match(/TRYB:\s*([^\n]+)/i)||[])[1]||'nieznany';
   const native=(switchport.match(/DEFAULT \/ NATIVE VLAN:\s*([^\n]+)/i)||[])[1]||'brak danych';
   const allowed=(switchport.match(/DOZWOLONE VLAN:\s*([^\n]+)/i)||[])[1]||'brak danych';
    const ingress=(switchport.match(/INGRESS FILTER:\s*([^\n]+)/i)||[])[1]||'Niedostępny';
    const frames=(switchport.match(/TYP RAMEK:\s*([^\n]+)/i)||[])[1]||'Niedostępny';
    showFriendlyResult(id,'Port '+port+' · tryb '+mode+' · VLAN domyślny '+native+' · dozwolone: '+allowed+' · ingress filter: '+ingress+' · typ ramek: '+frames+' · wpisy VLAN w tabeli: '+vlanLines.length,'Konfiguracja VLAN portu');
   continue;
  }
  if(key==='mac'){
   const portLines=portContextLines(output,port);
   const macs=[...new Set((portLines.join(' ').match(/\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b/ig)||[]).map(v=>v.toUpperCase()))];
   const summary=macs.length?'Wykryte adresy MAC ('+macs.length+'): '+macs.slice(0,8).join(', ')+(macs.length>8?' · pozostałe: '+(macs.length-8):''):'Brak adresów MAC przypisanych do tego portu w tablicy FDB.';
   showFriendlyResult(id,summary,label);continue;
  }
  const names=portContextLines(output,port).filter(Boolean);
  showFriendlyResult(id,names.length?'Wykryto '+names.length+' sąsiadów LLDP na tym porcie.':'Nie wykryto sąsiada LLDP.',label);
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
   'DOZWOLONE VLAN: '+((switchport.configuredVlans||[]).join(', ')||'brak danych'),
   'INGRESS FILTER: '+(switchport.ingressFilter||'Niedostępny'),
   'TYP RAMEK: '+(switchport.acceptableFrameTypes||'Niedostępny')
  ];
  const ctx={vlan:{output:vlanLines.join('\n')+'\n\n--- show vlan all ---\n'+(vlans.output||'')},mac:{output:mac.output||''},lldp:{output:lldp.output||''}};
  portNetworkContext.set(port,ctx);renderPortNetworkContext();
 }catch(e){
  portNetworkContext.set(port,{vlan:{error:e.message},mac:{error:e.message},lldp:{error:e.message}});
  renderPortNetworkContext();
 }finally{button.disabled=false;button.textContent='Odczytaj VLAN / MAC / LLDP';}
}
function renderPortDetail(){
 const name=$('detail-port')?.value,p=latestPorts.find(x=>x.name===name);
 if(!p){if($('detail-summary'))$('detail-summary').textContent='Wybierz port do diagnostyki.';for(const id of ['detail-admin-state','detail-oper-state','detail-speed-state','detail-mtu-state'])$(id).textContent='—';return;}
 for(const [id,val] of Object.entries({'detail-rx':p.inOctets,'detail-tx':p.outOctets,'detail-rxe':p.inErrors,'detail-txe':p.outErrors,'detail-rxd':p.inDiscards,'detail-txd':p.outDiscards,'detail-rxp':p.inUcastPkts,'detail-txp':p.outUcastPkts}))$(id).textContent=fmtCount(val);
 const r=portRates.get(name),cap=portCapacity(p),pct=r&&cap?Math.min(100,100*(r.rx+r.tx)/cap):null;
 $('detail-summary').textContent=name+' · '+(p.oper==='up'?'Połączony':p.oper==='down'?'Brak połączenia':'Stan nieznany')+' · '+(p.speed||'prędkość nieznana');
 $('detail-admin-state').textContent=p.admin==='up'?'Włączony':p.admin==='down'?'Wyłączony':'Niedostępny';
 $('detail-oper-state').textContent=p.oper==='up'?'Połączony':p.oper==='down'?'Brak linku':'Niedostępny';
 $('detail-speed-state').textContent=p.speed&&p.speed!=='unknown'?p.speed:'Niedostępna';
 $('detail-mtu-state').textContent=Number(p.mtu)>0?String(p.mtu)+' B':'Niedostępne';
 $('detail-live-rx').textContent=r?formatRate(r.rx):'Oczekiwanie na próbki';
 $('detail-live-tx').textContent=r?formatRate(r.tx):'Oczekiwanie na próbki';
 $('detail-utilization').textContent=pct===null?'—':pct.toFixed(1)+'% wykorzystania łącza';
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

function renderTopPorts(){const el=$('top-ports');if(!el)return;const rows=[...portRates.entries()].map(([name,r])=>({name,...r})).sort((a,b)=>(b.rx+b.tx)-(a.rx+a.tx)).slice(0,10);el.innerHTML=rows.length?rows.map(x=>'<tr><td><button class="port-diagnostic-link" data-diagnostic-port="'+esc(x.name)+'">'+esc(x.name)+'</button></td><td>'+(latestPorts.find(p=>p.name===x.name)?.oper==='up'?'Połączony':latestPorts.find(p=>p.name===x.name)?.oper==='down'?'Brak linku':'Nieznany')+'</td><td>'+formatRate(x.rx)+'</td><td>'+formatRate(x.tx)+'</td><td><b>'+formatRate(x.rx+x.tx)+'</b></td></tr>').join(''):'<tr><td colspan="5" class="empty">Zbieranie próbek — ranking pojawi się po drugim odczycie.</td></tr>';}
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
let statusLoading=false;async function load(){if(statusLoading)return;statusLoading=true;try{const d=await api('/api/status');latestPorts=sorted(d.ports||[]);document.dispatchEvent(new CustomEvent('ports:updated'));const up=latestPorts.filter(p=>p.oper==='up').length;$('host').textContent=d.host||'—';$('snmp-status').textContent=(d.snmpVersion==='3'?'SNMPv3':'SNMPv2c')+(d.reachable?' ONLINE':' OFFLINE');$('connection').classList.toggle('offline',!d.reachable);$('connection').innerHTML='<i></i> '+(d.reachable?'Połączono':'Brak połączenia');$('device-state').textContent=d.reachable?'ONLINE':'OFFLINE';$('device-state').classList.toggle('device-state-online',d.reachable);$('device-state').classList.toggle('device-state-offline',!d.reachable);$('count').textContent=latestPorts.length;$('up').textContent=up;$('updated').textContent=new Date().toLocaleTimeString();$('warning').textContent=d.warning||'';$('warning').classList.toggle('hidden',!d.warning);$('health-snmp').textContent=d.reachable?'Połączono':'Brak połączenia';$('health-snmp').className=d.reachable?'good':'bad';$('health-interfaces').textContent=latestPorts.length;$('health-up').textContent=up+' / '+latestPorts.length;$('health-time').textContent=new Date().toLocaleTimeString();$('map-online').textContent=(d.snmpVersion==='3'?'SNMPv3':'SNMPv2c')+(d.reachable?' ONLINE':' OFFLINE');syncPortChoices();render();renderMap();renderPortHistory();renderPortQuality();updateOverviewHealth();sampleTraffic();}catch(e){$('warning').textContent='Nie udało się pobrać danych switcha: '+e.message;$('warning').classList.remove('hidden');$('connection').classList.add('offline');$('connection').innerHTML='<i></i> Błąd odczytu';$('device-state').textContent='BRAK DANYCH';$('device-state').classList.remove('device-state-online');$('device-state').classList.add('device-state-offline');$('health-snmp').textContent='Błąd odczytu';$('health-snmp').className='bad';$('map-online').textContent='ODCZYT NIEUDANY';}finally{statusLoading=false;}}
function parseVlanSelection(value){
 const result=new Set();
 for(const token of String(value||'').split(/[\s,]+/).filter(Boolean)){
  const match=token.match(/^(\d{1,4})(?:-(\d{1,4}))?$/);
  if(!match)return null;
  const start=Number(match[1]),end=Number(match[2]||match[1]);
  if(start<1||end>4094||end<start||end-start>4093)return null;
  for(let vlan=start;vlan<=end;vlan++)result.add(vlan);
 }
 return [...result].sort((a,b)=>a-b);
}
$('bulk-apply').onclick=async()=>{
 const numbers=[...new Set($('bulk-ports').value.split(',').map(x=>x.trim()).filter(Boolean))];
 if(!numbers.length){alert('Wpisz numery portów, np. 1,2,3.');return;}
 if(numbers.some(n=>!/^[0-9]+$/.test(n)||Number(n)<1||Number(n)>52)){alert('Porty muszą być numerami od 1 do 52.');return;}
 const payload={ports:numbers.map(n=>'port1.0.'+n),mode:$('bulk-mode').value,accessVlan:Number($('bulk-access-vlan').value),allowed:$('bulk-allowed').value,native:Number($('bulk-native').value),speed:$('bulk-speed').value,duplex:$('bulk-duplex').value,description:$('bulk-description').value};
 if(payload.mode==='access'&&(!Number.isInteger(payload.accessVlan)||payload.accessVlan<1||payload.accessVlan>4094)){alert('Access VLAN musi mieścić się w zakresie 1–4094.');return;}
 if(payload.mode==='trunk'&&(!Number.isInteger(payload.native)||payload.native<1||payload.native>4094||!parseVlanSelection(payload.allowed)?.length)){alert('Dla trunk podaj poprawny Native VLAN i listę dozwolonych VLAN (np. 10,20 lub 10-20).');return;}
 const button=$('bulk-apply');button.disabled=true;
 try{
  const previewResult=await api('/api/ports/preview/bulk',payload);
  const previews=Array.isArray(previewResult.previews)?previewResult.previews:[];
  if(previews.length!==payload.ports.length)throw new Error('Podgląd zbiorczy nie zwrócił kompletu portów. Zapis został zablokowany.');
  const warnings=[...new Set(previews.flatMap(item=>Array.isArray(item.warnings)?item.warnings:[]))];
  const currentSummary=previews.map(item=>item.port+': '+(item.current?.mode||'tryb nieznany')+' · VLAN '+(item.current?.defaultVlan||'—')).join('\n');
  const summary='Profil '+(payload.mode==='access'?'Access':'Trunk')+' zostanie zastosowany na '+payload.ports.length+' portach:\n'+payload.ports.join(', ')+'\n\nStan bieżący:\n'+currentSummary+'\n\nNowe ustawienia: '+(payload.mode==='access'?'Access VLAN: '+payload.accessVlan:'Native VLAN: '+payload.native+' · dozwolone VLAN: '+payload.allowed)+'\n\nWszystkie porty sprawdzono jednym wspólnym odczytem konfiguracji switcha. Przed zmianą zostanie utworzona kopia konfiguracji.'+(warnings.length?'\n\nUwagi: '+warnings.join(' · '):'');
  showFriendlyResult('bulk-output',summary,'Podgląd profilu portów');
  if(!confirm(summary+'\n\nZastosować profil?'))return;
  payload.acknowledgeConnectivityRisk=true;
  const result=await api('/api/ports/settings/bulk',payload);
   let verification;
   try{verification=await api('/api/ports/switchport/bulk',{ports:payload.ports});}
   catch(error){throw new Error('Konfiguracja została wysłana, ale zbiorczy odczyt zwrotny nie powiódł się: '+error.message);}
   const states=Array.isArray(verification.ports)?verification.ports:[];
   const byPort=new Map(states.map(state=>[state.port,state]));
   const checks=payload.ports.map(port=>{
    const state=byPort.get(port);
    if(!state)return {port,ok:false,detail:'Brak odczytu zwrotnego'};
    const mode=String(state.mode||'').toLowerCase(),native=Number.parseInt(state.defaultVlan,10);
    const modeOK=mode===payload.mode;
    const vlanOK=payload.mode==='access'?native===payload.accessVlan:(native===payload.native&&Array.isArray(state.configuredVlans)&&parseVlanSelection(payload.allowed).every(v=>state.configuredVlans.map(Number).includes(v)));
    return {port,ok:modeOK&&vlanOK,detail:!modeOK?'Tryb: '+(mode||'nieustalony'):!vlanOK?'VLAN wymaga sprawdzenia':'Konfiguracja potwierdzona'};
   });
  const verified=checks.filter(x=>x.ok).length;
  const report='Weryfikacja po zastosowaniu: '+verified+'/'+checks.length+' portów potwierdzonych.\n'+checks.map(x=>(x.ok?'✓ ':'! ')+x.port+' — '+x.detail).join('\n');
  showFriendlyResult('bulk-output',report,'Wynik konfiguracji portów');
  addEvent('Profil '+payload.mode.toUpperCase()+' zastosowany na '+payload.ports.length+' portach; potwierdzono '+verified+'/'+checks.length,'warning');
  await load();
 }catch(e){showFriendlyResult('bulk-output',e.message,'Profil portów');}
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
 button.disabled=true;showFriendlyResult('bulk-admin-output','Operacja '+(action==='enable'?'włączania':'wyłączania')+' '+ports.length+' portów.','Operacja na portach');
 try{
  const result=await api('/api/ports/action/bulk',{ports,action});
  showFriendlyResult('bulk-admin-output',result.output,'Operacja na portach');
  addEvent((action==='enable'?'Włączono':'Wyłączono')+' porty: '+ports.join(', '),action==='enable'?'success':'warning');
  await load();
 }catch(e){showFriendlyResult('bulk-admin-output',e.message,'Operacja na portach');alert(e.message);}
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
 button.disabled=true;showFriendlyResult('bulk-edge-output','Operacja EdgePort dla '+ports.length+' portów.','Operacja EdgePort');
 try{
  const result=await api('/api/ports/edgeport/bulk',{ports,enabled});
  showFriendlyResult('bulk-edge-output',result.output,'Operacja EdgePort');
  addEvent((enabled?'Włączono':'Wyłączono')+' EdgePort: '+ports.join(', '),'warning');
  await load();
 }catch(e){showFriendlyResult('bulk-edge-output',e.message,'Operacja EdgePort');alert(e.message);}
 finally{button.disabled=false;}
};
$('lacp-create').onclick=async()=>{const group=Number($('lacp-group').value),ports=$('lacp-ports').value,mode=$('lacp-mode').value;if(!confirm('Utworzyć LACP group '+group+' na portach '+ports+'? Porty powinny być wcześniej skonfigurowane identycznie po obu stronach łącza.'))return;try{const d=await api('/api/lacp',{group,ports,mode,delete:false});showFriendlyResult('lacp-output',d.output,'Operacja LACP');}catch(e){showFriendlyResult('lacp-output',e.message,'Operacja LACP');}};
$('lacp-delete').onclick=async()=>{const group=Number($('lacp-group').value),ports=$('lacp-ports').value;if(!confirm('Usunąć konfigurację channel-group z portów '+ports+'?'))return;try{const d=await api('/api/lacp',{group,ports,mode:$('lacp-mode').value,delete:true});showFriendlyResult('lacp-output',d.output,'Operacja LACP');}catch(e){showFriendlyResult('lacp-output',e.message,'Operacja LACP');}};
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
$('apply-stp').onclick=async()=>{const mode=$('stp-mode').value,priority=Number($('stp-priority').value);if(!confirm('Zmienić globalny tryb STP na '+mode.toUpperCase()+' i priorytet bridge na '+priority+'?'))return;try{const d=await api('/api/stp',{mode,priority});showFriendlyResult('stp-output',d.output,'Operacja STP');}catch(e){alert(e.message);}};
$('show-stp').onclick=async()=>{try{const d=await api('/api/command',{command:'show spanning-tree'});showFriendlyResult('stp-output',d.output,'Stan STP');}catch(e){showFriendlyResult('stp-output',e.message,'Stan STP');}};
$('edgeport-toggle').onchange=async e=>{const port=$('edgeport-port').value,enabled=e.target.checked;if(!port){alert('Wybierz port edge.');e.target.checked=false;return;}if(!confirm((enabled?'Włączyć':'Wyłączyć')+' edgeport na '+port+'? Włączaj wyłącznie na portach końcowych.')){e.target.checked=!enabled;return;}try{const d=await api('/api/edgeport',{port,enabled});showFriendlyResult('stp-output',d.output,'Operacja STP');}catch(x){e.target.checked=!enabled;alert(x.message);}};

async function openPortDialog(port){
 const dialog=$('port-dialog');if(!dialog)return;
 $('cfg-port').value=port||$('cfg-port').value;
 const selected=$('cfg-port').value,current=latestPorts.find(p=>p.name===selected);
 const apply=$('apply-port-settings'),status=$('cfg-load-status');
 $('cfg-description').value=current?.description||'';
 $('cfg-mode').value='access';$('cfg-access-vlan').value='1';$('cfg-allowed').value='';$('cfg-native').value='1';
 $('cfg-access-vlan').disabled=false;$('cfg-allowed').disabled=true;$('cfg-native').disabled=true;
 $('cfg-speed').value='auto';$('cfg-duplex').value='auto';
 apply.disabled=true;status.classList.remove('is-error');status.textContent='Odczytuję bieżącą konfigurację '+selected+'…';
 if(typeof dialog.showModal==='function')dialog.showModal();else dialog.setAttribute('open','');
 try{
  const details=await api('/api/port/switchport?port='+encodeURIComponent(selected));
  const mode=String(details.mode||'').toLowerCase();
  const native=Number.parseInt(details.defaultVlan,10);
  if(!['access','trunk'].includes(mode))throw new Error('Nie rozpoznano trybu switchport. Edycja została zablokowana, aby nie nadpisać nieznanej konfiguracji.');
  if(!Number.isInteger(native)||native<1||native>4094)throw new Error('Nie udało się wiarygodnie odczytać domyślnego/native VLAN. Edycja została zablokowana.');
  $('cfg-mode').value=mode;$('cfg-native').value=String(native);
  if(mode==='access')$('cfg-access-vlan').value=String(native);
  else{
   if(!Array.isArray(details.configuredVlans)||!details.configuredVlans.length)throw new Error('Nie udało się odczytać listy VLAN dozwolonych na trunk. Edycja została zablokowana.');
   $('cfg-allowed').value=details.configuredVlans.join(',');
  }
  $('cfg-access-vlan').disabled=mode==='trunk';$('cfg-allowed').disabled=mode!=='trunk';$('cfg-native').disabled=mode!=='trunk';
  if(current?.speed){const speed=String(current.speed).match(/(10|100|1000)\s*Mb/i);if(speed)$('cfg-speed').value=speed[1];}
  apply.disabled=false;
  status.textContent='Odczytano bieżącą konfigurację: '+mode.toUpperCase()+' · '+(mode==='access'?'Access VLAN '+native:'Native VLAN '+native+' · dozwolone VLAN: '+details.configuredVlans.join(', '))+'. Sprawdź wartości przed zapisaniem.';
 }catch(e){
  status.classList.add('is-error');status.textContent='Nie można bezpiecznie wczytać konfiguracji portu: '+e.message+' Zmiany są zablokowane.';
 }
}
$('close-port-dialog').onclick=()=>{const d=$('port-dialog');if(d.close)d.close();else d.removeAttribute('open');};
$('port-dialog').addEventListener('click',e=>{if(e.target===$('port-dialog'))$('port-dialog').close();});
$('cfg-mode').onchange=()=>{const trunk=$('cfg-mode').value==='trunk';$('cfg-access-vlan').disabled=trunk;$('cfg-allowed').disabled=!trunk;$('cfg-native').disabled=!trunk;};
$('ports').addEventListener('click',e=>{const b=e.target.closest('[data-config-port]');if(!b)return;openPortDialog(b.dataset.configPort);});
// Mapa portów otwiera diagnostykę; konfiguracja pozostaje dostępna z listy portów.
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
 const [d,capabilityResult]=await Promise.all([
   api('/api/system'),
   api('/api/device/capabilities').then(value=>({value})).catch(error=>({error}))
  ]);
  const capabilities=capabilityResult.value;
  const identity=capabilities?.identity;
  $('sys-snmp-name').textContent=identity?.name||'Brak danych SNMP';
  $('sys-snmp-uptime').textContent=identity?.uptime||'Brak danych SNMP';
  $('sys-snmp-objectid').textContent=identity?.objectId||'Brak danych SNMP';
  $('sys-snmp-description').textContent=identity?.description||capabilityResult.error?.message||'Brak danych SNMP';
  $('sys-discovered-ports').textContent=Number.isFinite(capabilities?.interfacesObserved)?String(capabilities.interfacesObserved):'Brak danych SNMP';
  const speedSummary=capabilities?.interfaceSpeeds||{};
  $('sys-interface-speeds').textContent=Object.keys(speedSummary).length?Object.entries(speedSummary).sort((a,b)=>a[0].localeCompare(b[0],undefined,{numeric:true})).map(([speed,count])=>`${speed}: ${count}`).join(' · '):'Brak danych SNMP';
 const versionText=String(d.version||'');
 const versionMatch=versionText.match(/\b\d+(?:\.\d+){1,3}(?:[-+._A-Za-z0-9]*)?\b/);
 $('sys-version').textContent=versionMatch?versionMatch[0]:'Informacja niedostępna';
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
$('alert-source-filter')?.addEventListener('change',renderEvents);
$('alert-port-filter')?.addEventListener('change',renderEvents);
$('alert-search')?.addEventListener('input',renderEvents);
$('alert-refresh')?.addEventListener('click',async()=>{const b=$('alert-refresh');b.disabled=true;try{await restoreMonitorHistory();}finally{b.disabled=false;}});

const vlanSelectedPorts=new Set();
function updateVLANSelection(){const count=$('vlan-select-count');if(count)count.textContent='Zaznaczono: '+vlanSelectedPorts.size;const use=$('vlan-use-selected');if(use)use.disabled=vlanSelectedPorts.size===0;const apply=$('vlan-apply-membership');if(apply)apply.disabled=vlanSelectedPorts.size===0;}
async function inspectVLAN(){
 const id=Number($('vlan-inspect-id').value),status=$('vlan-inspect-status'),map=$('vlan-port-map');
 if(!Number.isInteger(id)||id<1||id>4094){alert('VLAN ID musi być w zakresie 1–4094.');return;}
 vlanSelectedPorts.clear();updateVLANSelection();map.innerHTML='';status.textContent='Sprawdzanie dostępności switcha i odświeżanie portów…';
 try{
  const state=await api('/api/status');
  if(!state.reachable)throw new Error('Switch jest nieosiągalny.');
  if(!Array.isArray(state.ports)||!state.ports.length||!state.ports.every(p=>p&&p.name))throw new Error('Brak kompletnej listy portów z aktualnego odczytu.');
  latestPorts=sorted(state.ports);
  const d=await api('/api/command',{command:'show vlan '+id}),raw=String(d.output||'').trim();
  if(!raw)throw new Error('Switch zwrócił pustą odpowiedź dla VLAN '+id+'.');
  const members=new Map(),knownPorts=new Set(latestPorts.map(p=>p.name.toLowerCase()));
  for(const m of raw.matchAll(/(port\d+\.\d+\.\d+)\s*\(([ut])\)/gi)){const name=m[1].toLowerCase();if(knownPorts.has(name))members.set(name,m[2].toLowerCase());}
  $('vlan-inspect-raw').textContent=raw;
  const ports=latestPorts.map(p=>{const mode=members.get(p.name)||'unknown';return '<div class="vlan-port-cell '+(mode==='u'?'is-untagged':mode==='t'?'is-tagged':'is-unknown')+'" data-vlan-state="'+mode+'" data-vlan-port-name="'+esc(p.name.toLowerCase())+'"><b>'+esc(p.name.replace(/^port1\.0\./,''))+'</b><small>'+(mode==='u'?'UNTAGGED':mode==='t'?'TAGGED':'NIEUSTALONY')+'</small><label class="vlan-port-select"><input type="checkbox" data-vlan-select="'+esc(p.name)+'"> Zaznacz</label><button type="button" class="vlan-port-config" data-vlan-port="'+esc(p.name)+'" aria-label="Konfiguruj port '+esc(p.name)+'">Konfiguruj</button></div>';});
  map.innerHTML=ports.join('')||'<p class="empty">Brak portów.</p>';
  applyVLANPortFilters();
  const tagged=[...members.values()].filter(v=>v==='t').length,untagged=[...members.values()].filter(v=>v==='u').length;
  const unknown=latestPorts.length-members.size;status.textContent=members.size?'VLAN '+id+': rozpoznano '+members.size+' przypisań ('+tagged+' tagged, '+untagged+' untagged). Porty bez dopasowania: '+unknown+' — ich członkostwo jest nieustalone, nie oznacza to braku VLAN.':'Odczytano odpowiedź VLAN '+id+', ale parser nie rozpoznał formatu członkostwa. Wszystkie porty mają stan nieustalony; sprawdź szczegóły odpowiedzi poniżej.';
 }catch(e){$('vlan-inspect-raw').textContent='';status.textContent='Nie udało się wiarygodnie odczytać VLAN: '+e.message;map.innerHTML='<p class="empty">Brak aktualnych danych — mapa VLAN nie została zaktualizowana.</p>';}
}

function applyVLANPortFilters(){
 const root=$('vlan-port-map'),query=String($('vlan-port-search')?.value||'').trim().toLowerCase(),state=$('vlan-state-filter')?.value||'all';
 const cells=[...root.querySelectorAll('.vlan-port-cell')];let visible=0,tagged=0,untagged=0,unknown=0;
 for(const cell of cells){const mode=cell.dataset.vlanState||'unknown',name=cell.dataset.vlanPortName||'',matchState=state==='all'||mode===state,matchName=!query||name.includes(query);const show=matchState&&matchName;cell.hidden=!show;if(show){visible++;if(mode==='t')tagged++;else if(mode==='u')untagged++;else unknown++;}}
 const summary=$('vlan-map-count');if(summary)summary.textContent=cells.length?'Widoczne: '+visible+' / '+cells.length+' · Tagged: '+tagged+' · Untagged: '+untagged+' · Nieustalone: '+unknown:'Brak aktualnego odczytu.';
}
$('vlan-port-search').addEventListener('input',applyVLANPortFilters);
$('vlan-state-filter').addEventListener('change',applyVLANPortFilters);
$('inspect-vlan').onclick=inspectVLAN;
$('vlan-port-map').addEventListener('click',e=>{const button=e.target.closest('[data-vlan-port]');if(!button)return;openPortDialog(button.dataset.vlanPort);});
$('vlan-port-map').addEventListener('change',e=>{const input=e.target.closest('[data-vlan-select]');if(!input)return;if(input.checked)vlanSelectedPorts.add(input.dataset.vlanSelect);else vlanSelectedPorts.delete(input.dataset.vlanSelect);updateVLANSelection();});
$('vlan-select-all').onclick=()=>{for(const cell of $('vlan-port-map').querySelectorAll('.vlan-port-cell:not([hidden])')){const input=cell.querySelector('[data-vlan-select]');if(input){input.checked=true;vlanSelectedPorts.add(input.dataset.vlanSelect);}}updateVLANSelection();};
$('vlan-select-clear').onclick=()=>{vlanSelectedPorts.clear();for(const input of $('vlan-port-map').querySelectorAll('[data-vlan-select]'))input.checked=false;updateVLANSelection();};

async function applyVLANMembership(){
 const ports=[...vlanSelectedPorts].sort(),vlan=Number($('vlan-inspect-id').value),membership=$('vlan-membership-mode').value,output=$('vlan-membership-output'),button=$('vlan-apply-membership');
 if(!ports.length){alert('Zaznacz co najmniej jeden port.');return;}
 if(!Number.isInteger(vlan)||vlan<1||vlan>4094){alert('VLAN ID musi być w zakresie 1–4094.');return;}
 const payload={ports,vlan,membership};button.disabled=true;
 try{
  const preview=await api('/api/vlan/ports/preview',payload);
  const summary='Zmiana członkostwa '+membership.toUpperCase()+' dla VLAN '+vlan+' na '+ports.length+' portach. Podgląd nie zmienia konfiguracji.'+(preview.warnings?.length?'\n\nUwagi: '+preview.warnings.join(' · '):'');
  showFriendlyResult('vlan-membership-output',summary,'Podgląd zmiany VLAN');
  if(!confirm(summary+'\n\nZastosować zmianę?'))return;
  await api('/api/vlan/ports/apply',payload);
  let good=0;
  try{const check=await api('/api/command',{command:'show vlan '+vlan}),observed=new Map();for(const match of String(check.output||'').matchAll(/(port\d+\.\d+\.\d+)\s*\(([ut])\)/gi))observed.set(match[1],match[2].toLowerCase());const expected=membership==='tagged'?'t':'u';good=ports.filter(port=>observed.get(port)===expected).length;}
  catch(_e){}
  showFriendlyResult('vlan-membership-output','Zweryfikowano '+good+' z '+ports.length+' portów. Oczekiwany tryb: '+membership.toUpperCase()+', VLAN '+vlan+'.','Weryfikacja członkostwa VLAN');
  addEvent('Zmieniono członkostwo VLAN '+vlan+' na '+ports.length+' portach');await inspectVLAN();
 }catch(e){showFriendlyResult('vlan-membership-output',e.message,'Operacja VLAN');}
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
 const rows=[],macPattern=/(?:[0-9a-f]{4}\.){2}[0-9a-f]{4}|(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}|\b[0-9a-f]{12}\b/ig,portPattern=/\b(?:port\d+\.\d+\.\d+|sa\d+|po\d+)\b/i;
 for(const line of String(raw||'').split(/\r?\n/)){const mm=line.match(macPattern),pm=line.match(portPattern);if(!mm||!pm)continue;const mac=normalizeMAC(mm[0]);if(!mac)continue;const beforeMAC=line.slice(0,line.toLowerCase().indexOf(mm[0].toLowerCase())).trim(),vlanMatch=beforeMAC.match(/^(?:vlan\s*)?(\d{1,4})(?:\s|$)/i);rows.push({mac,port:pm[0],vlan:vlanMatch?vlanMatch[1]:'',source:line.trim()});}
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
  for(const m of raw.matchAll(/\b(port\d+\.\d+\.\d+|sa\d+|po\d+)\s*\(([ut])\)/gi))members.set(m[1],m[2].toLowerCase());
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
  $('topology-status').textContent='Odczyt '+new Date().toLocaleString()+' · FDB: '+fdb.length+' wpisów · ARP: '+[...arp.values()].reduce((n,x)=>n+x.length,0)+' adresów · LLDP: '+[...lldp.values()].reduce((n,x)=>n+x.length,0)+' obserwacji'+(errors.length?' · Nie udało się odczytać '+errors.length+' źródeł':' · Odczyt zakończony');
  renderTopology();
 }finally{button.disabled=false;button.textContent='Skanuj ponownie';}
}
$('refresh-topology').onclick=refreshTopology;
$('topology-filter').addEventListener('input',renderTopology);$('topology-class-filter').addEventListener('change',renderTopology);
$('topology-rows').addEventListener('click',e=>{const b=e.target.closest('[data-topology-port]');if(b)openPortDiagnostics(b.dataset.topologyPort);});
async function refreshDHCP(){
 for(const [cmd,id,label] of [['show ip dhcp server summary','dhcp-summary','Usługa DHCP'],['show ip dhcp binding','dhcp-bindings','Dzierżawy DHCP']]){
  try{const d=await api('/api/command',{command:cmd});showFriendlyResult(id,d.output,label);}
  catch(e){showFriendlyResult(id,e.message,label);}
 }
}
$('dhcp-refresh').onclick=refreshDHCP;


let arpTableText='';
async function refreshL3(){
 for(const [cmd,id,label] of [['show ip interface brief','l3-interfaces','Interfejsy IP'],['show ip route','l3-routes','Trasy routingu'],['show arp','l3-arp','Sąsiedzi ARP']]){
  try{const d=await api('/api/command',{command:cmd});if(id==='l3-arp')arpTableText=d.output||'';showFriendlyResult(id,d.output,label);}
  catch(e){if(id==='l3-arp')arpTableText='';showFriendlyResult(id,e.message,label);}
 }
}
function filterARP(){const q=$('arp-filter').value.trim().toLowerCase(),matches=arpTableText.split(/\r?\n/).filter(line=>!q||line.toLowerCase().includes(q));showFriendlyResult('l3-arp',matches.join('\n'),'Wyniki wyszukiwania sąsiadów IP');}


async function configureStaticRoute(remove){const q={prefix:$('route-prefix').value.trim(),gateway:$('route-gateway').value.trim(),distance:Number($('route-distance').value),delete:remove};const action=remove?'usunąć':'dodać';if(!confirm('Czy na pewno '+action+' trasę '+q.prefix+' przez '+q.gateway+'? Zmiana może wpłynąć na łączność z przełącznikiem.'))return;try{const d=await api('/api/route/static',q);showFriendlyResult('probe-output',d.output,'Konfiguracja trasy');await refreshL3();}catch(e){alert(e.message);}}
$('route-add').onclick=()=>configureStaticRoute(false);
$('route-delete').onclick=()=>configureStaticRoute(true);
async function runProbe(kind){const target=$('probe-target').value.trim();$('probe-output').textContent=(kind==='ping'?'Ping':'Traceroute')+' '+target+'…';try{const d=await api('/api/network/probe',{kind,target});showFriendlyResult('probe-output',d.output,'Wynik testu łączności');}catch(e){showFriendlyResult('probe-output',e.message,'Test łączności');}}
$('probe-ping').onclick=()=>runProbe('ping');
$('probe-trace').onclick=()=>runProbe('traceroute');

async function refreshSNMP(){
 const jobs=[['show snmp-server','snmp-agent','Agent SNMP'],['show snmp-server community','snmp-communities','Dostęp SNMP'],['show snmp-server user','snmp-users','Użytkownicy SNMPv3'],['show counter snmp-server','snmp-counters','Statystyki SNMP'],['show running-config snmp','snmp-running','Ustawienia SNMP']];
 for(const [command,id,label] of jobs){try{const d=await api('/api/command',{command});showFriendlyResult(id,d.output,label);}catch(e){showFriendlyResult(id,e.message,label);}}
}

$('snmp-refresh').onclick=refreshSNMP;

async function refreshSSH(){
 for(const [command,id,label] of [['show users','ssh-users','Konta i sesje'],['show ssh server','ssh-status','Usługa SSH'],['show running-config ssh','ssh-config','Ustawienia SSH']]){
  try{const d=await api('/api/command',{command});showFriendlyResult(id,d.output,label);}catch(e){showFriendlyResult(id,e.message,label);}
 }
}

$('ssh-refresh').onclick=refreshSSH;
async function changeSSHUser(remove){const q={username:$('ssh-new-user').value.trim(),password:$('ssh-new-password').value,privilege:Number($('ssh-new-privilege').value),delete:remove};if(!q.username){alert('Wpisz login.');return;}if(!remove&&!q.password){alert('Wpisz nowe hasło.');return;}if(!confirm((remove?'Usunąć':'Utworzyć lub zaktualizować')+' konto '+q.username+'?'))return;const b=remove?$('ssh-user-delete'):$('ssh-user-save');b.disabled=true;try{const d=await api('/api/ssh/user',q);showFriendlyResult('ssh-action-output',d.output,'Zarządzanie kontem SSH');$('ssh-new-password').value='';await refreshSSH();}catch(e){showFriendlyResult('ssh-action-output',e.message,'Konto SSH');}finally{b.disabled=false;}}
$('ssh-user-save').onclick=()=>changeSSHUser(false);
$('ssh-user-delete').onclick=()=>changeSSHUser(true);

async function changeSNMPTrap(remove){const q={address:$('snmp-trap-ip').value.trim(),community:$('snmp-trap-community').value,delete:remove};if(!q.community){alert('Wpisz istniejącą community SNMP.');return;}if(!confirm((remove?'Usunąć':'Dodać')+' odbiorcę Trap '+q.address+' dla SNMPv2c?'))return;const b=remove?$('snmp-trap-remove'):$('snmp-trap-add');b.disabled=true;try{const d=await api('/api/snmp/trap-host',q);showFriendlyResult('snmp-action-output',d.output,'Konfiguracja powiadomień SNMP');await refreshSNMP();}catch(e){showFriendlyResult('snmp-action-output',e.message,'Operacja SNMP');}finally{b.disabled=false;}}
$('snmp-trap-add').onclick=()=>changeSNMPTrap(false);
$('snmp-trap-remove').onclick=()=>changeSNMPTrap(true);
async function changeSNMPv3User(remove){
 const q={username:$('snmp-v3-user').value.trim(),group:$('snmp-v3-group').value.trim(),authPassword:$('snmp-v3-auth').value,privacyPassword:$('snmp-v3-privacy').value,privacyProtocol:($('snmp-v3-priv-protocol')?.value||'aes'),delete:remove};
 if(!q.username){alert('Wpisz nazwę użytkownika SNMPv3.');return;}
 if(!remove&&(!q.authPassword||!q.privacyPassword)){alert('Wpisz oba hasła SNMPv3.');return;}
 if(!confirm((remove?'Usunąć':'Utworzyć')+' użytkownika SNMPv3 '+q.username+(remove?'?':' z SHA + '+q.privacyProtocol.toUpperCase()+' (authPriv)?')))return;
 const b=remove?$('snmp-v3-remove'):$('snmp-v3-add');b.disabled=true;
 try{const d=await api('/api/snmp/v3-user',q);showFriendlyResult('snmp-v3-output',d.output,'Użytkownik SNMPv3');$('snmp-v3-auth').value='';$('snmp-v3-privacy').value='';await refreshSNMP();}
 catch(e){showFriendlyResult('snmp-v3-output',e.message,'Użytkownik SNMPv3');}
 finally{b.disabled=false;}
}
$('snmp-v3-add').onclick=()=>changeSNMPv3User(false);
$('snmp-v3-remove').onclick=()=>changeSNMPv3User(true);
async function changeSNMPv3Trap(remove){
 const q={address:$('snmp-v3-trap-ip').value.trim(),username:$('snmp-v3-trap-user').value.trim(),delete:remove};
 if(!q.address||!q.username){alert('Wpisz adres IPv4 i użytkownika SNMPv3.');return;}
 if(!confirm((remove?'Usunąć':'Dodać')+' odbiorcę Trap SNMPv3 '+q.address+' ('+q.username+')?'))return;
 const b=remove?$('snmp-v3-trap-remove'):$('snmp-v3-trap-add');b.disabled=true;
 try{const d=await api('/api/snmp/v3-trap-host',q);showFriendlyResult('snmp-v3-trap-output',d.output,'Odbiorca Trap SNMPv3');await refreshSNMP();}
 catch(e){showFriendlyResult('snmp-v3-trap-output',e.message,'Odbiorca Trap SNMPv3');}
 finally{b.disabled=false;}
}
$('snmp-v3-trap-add').onclick=()=>changeSNMPv3Trap(false);
$('snmp-v3-trap-remove').onclick=()=>changeSNMPv3Trap(true);


$('refresh-l3').onclick=refreshL3;
$('arp-filter').addEventListener('input',filterARP);

$('dhcp-create').onclick=async()=>{const q={name:$('dhcp-name').value,network:$('dhcp-network').value,mask:$('dhcp-mask').value,low:$('dhcp-low').value,high:$('dhcp-high').value,router:$('dhcp-router').value,dns:$('dhcp-dns').value,enable:true};if(!confirm('Utworzyć pulę DHCP '+q.name+' i włączyć usługę DHCP Server? Upewnij się, że nie działa drugi serwer DHCP w tej sieci.'))return;const b=$('dhcp-create');b.disabled=true;try{const d=await api('/api/dhcp/pool',q);showFriendlyResult('dhcp-summary',d.output,'Konfiguracja puli DHCP');await refreshDHCP();}catch(e){alert(e.message);}finally{b.disabled=false;}};

async function loadDiagnostic(command,target){
 const label=command.includes('log')?'Zdarzenia systemowe':'Informacje o urządzeniu';
 try{const d=await api('/api/command',{command});showFriendlyResult(target,d.output,label);}
 catch(e){showFriendlyResult(target,e.message,label);}
}
$('load-system').onclick=async()=>{await loadDiagnostic('show system','system-output');};

let macFDBText='';
async function refreshFDB(){
 try{const d=await api('/api/command',{command:'show mac address-table'});macFDBText=d.output||'';showFriendlyResult('mac-output',macFDBText,'Tablica urządzeń MAC');showFriendlyResult('mac-table-output',macFDBText,'Urządzenia w sieci');}
 catch(e){showFriendlyResult('mac-output',e.message,'Tablica urządzeń MAC');showFriendlyResult('mac-table-output',e.message,'Urządzenia w sieci');}
}
function filterFDB(){const q=$('mac-filter').value.trim().toLowerCase(),matches=macFDBText.split(/\r?\n/).filter(line=>!q||line.toLowerCase().includes(q));showFriendlyResult('mac-table-output',matches.join('\n'),'Wyniki wyszukiwania urządzeń');}

$('load-mac').onclick=refreshFDB;
$('mac-filter').addEventListener('input',filterFDB);
$('load-lldp').onclick=async()=>{try{const d=await api('/api/command',{command:'show lldp neighbors'});showFriendlyResult('lldp-output',d.output,'Sąsiedzi sieciowi');}catch(e){showFriendlyResult('lldp-output',e.message,'Sąsiedzi sieciowi');}};

$('load-logs').onclick=()=>loadDiagnostic('show log tail 30','logs-output');
$('create-vlan').onclick=async()=>{
 const id=Number($('vlan-id').value),name=$('vlan-name').value.trim();
 try{const p=await api('/api/vlan/preview',{id,name,delete:false});const summary='Utworzenie VLAN '+id+(name?' („'+name+'”)':'')+'. Podgląd nie zmienia konfiguracji.'+(p.warnings?.length?'\n\nUwagi: '+p.warnings.join(' · '):'');showFriendlyResult('vlan-output',summary,'Podgląd VLAN');if(!confirm(summary+'\n\nZastosować zmianę?'))return;const d=await api('/api/vlan',{id,name,delete:false});showFriendlyResult('vlan-output',d.output,'Utworzenie VLAN');alert('Zlecenie utworzenia VLAN przekazane. Odśwież listę, aby potwierdzić stan.');}catch(e){showFriendlyResult('vlan-output',e.message,'Operacja VLAN');}
};
$('delete-vlan').onclick=async()=>{
 const id=Number($('vlan-id').value);
 try{const p=await api('/api/vlan/preview',{id,delete:true});const summary='Usunięcie VLAN '+id+'. Podgląd nie zmienia konfiguracji.'+(p.warnings?.length?'\n\nUwagi: '+p.warnings.join(' · '):'');showFriendlyResult('vlan-output',summary,'Podgląd usunięcia VLAN');if(!confirm(summary+'\n\nCzy na pewno usunąć VLAN?'))return;const d=await api('/api/vlan',{id,delete:true});showFriendlyResult('vlan-output',d.output,'Usunięcie VLAN');}catch(e){showFriendlyResult('vlan-output',e.message,'Operacja VLAN');}
};
$('show-vlans').onclick=async()=>{
 const button=$('show-vlans'),summary=$('vlan-list-summary');
 button.disabled=true;button.textContent='Odczyt…';summary.textContent='Pobieranie listy VLAN ze switcha…';
 try{
  const d=await api('/api/command',{command:'show vlan all'});
  const raw=String(d.output||'').trim();
  if(!raw)throw new Error('Switch zwrócił pustą odpowiedź.');
  $('vlan-output').textContent=raw;
  summary.textContent='Odczyt zakończony — switch zwrócił odpowiedź. Nie pokazuję liczby VLAN, ponieważ format odpowiedzi nie został jeszcze zweryfikowany na tym modelu. Pełna odpowiedź urządzenia jest dostępna w szczegółach poniżej.';
 }catch(e){$('vlan-output').textContent='';summary.textContent='Nie udało się odczytać VLAN: '+e.message;}
 finally{button.disabled=false;button.textContent='Pokaż konfigurację VLAN';}
};
$('apply-port-settings').onclick=async()=>{
 const payload={port:$('cfg-port').value,mode:$('cfg-mode').value,accessVlan:Number($('cfg-access-vlan').value),allowed:$('cfg-allowed').value,native:Number($('cfg-native').value),speed:$('cfg-speed').value,duplex:$('cfg-duplex').value,description:$('cfg-description').value};
 if(!payload.port){alert('Wybierz port.');return;}
 if(payload.mode==='access'&&(!Number.isInteger(payload.accessVlan)||payload.accessVlan<1||payload.accessVlan>4094)){alert('Access VLAN musi mieścić się w zakresie 1–4094.');return;}
 if(payload.mode==='trunk'&&(!Number.isInteger(payload.native)||payload.native<1||payload.native>4094||!parseVlanSelection(payload.allowed)?.length)){alert('Dla trunk podaj poprawny Native VLAN i listę dozwolonych VLAN.');return;}
 const button=$('apply-port-settings');button.disabled=true;
 try{
  const p=await api('/api/port/preview',payload);
  const current=p.current||{};
  const summary='Port '+payload.port+' — stan bieżący: '+(current.mode||'nieustalony')+', VLAN '+(current.defaultVlan||'—')+'.\nNowy tryb: '+(payload.mode==='access'?'Access':'Trunk')+'. '+(payload.mode==='access'?'Access VLAN: '+payload.accessVlan:'Native VLAN: '+payload.native+' · dozwolone VLAN: '+payload.allowed)+'. Podgląd nie zmienia konfiguracji.'+(p.warnings?.length?'\n\nUwagi: '+p.warnings.join(' · '):'');
  showFriendlyResult('output',summary,'Podgląd portu');
  if(!confirm(summary+'\n\nZastosować konfigurację?'))return;
  payload.acknowledgeConnectivityRisk=true;
  await api('/api/port/settings',payload);
  const state=await api('/api/port/switchport?port='+encodeURIComponent(payload.port));
  const modeOK=String(state.mode||'').toLowerCase()===payload.mode,native=Number.parseInt(state.defaultVlan,10);
  const vlanOK=payload.mode==='access'?native===payload.accessVlan:(native===payload.native&&Array.isArray(state.configuredVlans)&&parseVlanSelection(payload.allowed).every(v=>state.configuredVlans.map(Number).includes(v)));
  if(!modeOK||!vlanOK)throw new Error('Switch przyjął zlecenie, ale odczyt zwrotny nie potwierdził wszystkich ustawień. Sprawdź konfigurację portu przed kolejną zmianą.');
  showFriendlyResult('output','Konfiguracja portu '+payload.port+' została odczytana ponownie i potwierdzona.','Konfiguracja portu');
  addEvent('Potwierdzono konfigurację '+payload.port,'success');
  $('port-dialog').close();await load();
 }catch(e){showFriendlyResult('output',e.message,'Konfiguracja portu');}
 finally{button.disabled=false;}
};
$('detail-port').addEventListener('change',renderPortDetail);
$('load-port-network').addEventListener('click',loadPortNetworkContext);
$('port-map').addEventListener('click',e=>{const b=e.target.closest('[data-map-port]');if(b)openPortDiagnostics(b.dataset.mapPort);});
$('top-ports').addEventListener('click',e=>{const b=e.target.closest('[data-diagnostic-port]');if(b)openPortDiagnostics(b.dataset.diagnosticPort);});
$('refresh').onclick=load;$('filter').oninput=render;$('state-filter').onchange=render;$('port-map-filter').onchange=renderMap;
$('ports').addEventListener('click',async e=>{const diagnostic=e.target.closest('[data-config-port]');if(diagnostic)return;const b=e.target.closest('[data-port]');if(!b)return;const action=b.dataset.action,port=b.dataset.port;if(!confirm((action==='disable'?'Wyłączyć':'Włączyć')+' port '+port+'?'))return;b.disabled=true;try{const d=await api('/api/port',{port,action});showFriendlyResult('output',d.output,'Zmiana stanu portu');addEvent('Port '+port+' — '+(action==='disable'?'wyłączono':'włączono'));await load();}catch(x){alert('Nie udało się zmienić portu: '+x.message);}finally{b.disabled=false;}});
$('dhcp-toggle').onchange=async e=>{const enabled=e.target.checked;if(!confirm((enabled?'Włączyć':'Wyłączyć')+' klienta DHCP na VLAN 1? Zmiana adresu może zerwać połączenie.')){e.target.checked=!enabled;return;}e.target.disabled=true;try{const d=await api('/api/management-dhcp',{enabled});showFriendlyResult('output',d.output,'Zmiana stanu portu');alert('Polecenie przekazane. Sprawdź adres zarządzający switcha.');}catch(x){e.target.checked=!enabled;alert(x.message);}finally{e.target.disabled=false;}};
async function refreshAudit(){const body=$('audit-rows');body.innerHTML='<tr><td colspan="6" class="empty">Odczyt dziennika…</td></tr>';try{const items=await api('/api/audit');body.innerHTML=items.length?items.slice().reverse().map(x=>{const status=Number(x.status)||0;return '<tr><td>'+esc(new Date(x.timestamp).toLocaleString())+'</td><td>'+esc(x.username)+'</td><td><b>'+esc(x.method)+'</b></td><td><code>'+esc(x.path)+'</code></td><td><span class="state '+(status>=500?'down':status>=400?'unknown':'up')+'">'+status+'</span></td><td>'+fmtCount(x.durationMs)+' ms</td></tr>';}).join(''):'<tr><td colspan="6" class="empty">Brak zarejestrowanych operacji.</td></tr>';}catch(e){body.innerHTML='<tr><td colspan="6" class="empty">'+esc(e.message)+'</td></tr>';}}
$('refresh-audit').onclick=refreshAudit;
function renderConfigBackups(items){const el=$('config-backups');if(!items.length){el.textContent='Brak zapisanych kopii.';return;}el.innerHTML=items.map(x=>'<div class="health-line"><span><b>'+esc(new Date(x.createdAt).toLocaleString())+'</b><small> · '+fmtCount(x.size)+' B · Integralność: '+(x.verified?'potwierdzona':'niepotwierdzona')+'</small><br><small class="'+(x.verified?'good':'bad')+'">'+(x.verified?'SHA-256 zweryfikowany':'NIEZWERYFIKOWANA KOPIA')+'</small></span><span class="backup-actions">'+(x.verified?'<button class="btn ghost" type="button" data-compare-backup="'+esc(x.id)+'">Porównaj z bieżącą</button> <button class="btn ghost" type="button" data-restore-preview="'+esc(x.id)+'">Sprawdź kopię</button>':'')+' <a class="btn ghost" href="/api/config/backups/download?id='+encodeURIComponent(x.id)+'">Pobierz</a></span></div>').join('');}
async function refreshConfigBackups(){const el=$('config-backups');el.textContent='Odczyt historii kopii…';try{const r=await fetch('/api/config/backups');if(!r.ok)throw new Error('HTTP '+r.status);renderConfigBackups(await r.json());}catch(e){el.textContent='Nie udało się odczytać historii: '+e.message;}}
$('config-backups').addEventListener('click',async event=>{const button=event.target.closest('[data-compare-backup]');if(!button)return;const id=button.dataset.compareBackup,box=$('config-diff');button.disabled=true;button.textContent='Porównuję…';box.textContent='Sprawdzanie integralności kopii i porównywanie z bieżącą konfiguracją…';try{const response=await fetch('/api/config/backups/compare?id='+encodeURIComponent(id),{cache:'no-store'});if(!response.ok)throw new Error('Nie udało się porównać kopii: HTTP '+response.status);const d=await response.json();box.textContent=d.identical?'Kopia '+d.backupId+' jest zgodna z bieżącą konfiguracją.':'Kopia '+d.backupId+' różni się od bieżącej konfiguracji · Dodane od czasu kopii: '+d.addedInCurrent+' · Brakujące względem kopii: '+d.removedFromCurrent+' · Linie: kopia '+d.backupLines+' / bieżąca '+d.currentLines+' · integralność kopii potwierdzona';}catch(e){box.textContent=e.message;}finally{button.disabled=false;button.textContent='Porównaj z bieżącą';}});$('config-backups').addEventListener('click',async event=>{const button=event.target.closest('[data-restore-preview]');if(!button)return;const id=button.dataset.restorePreview,box=$('config-diff');button.disabled=true;button.textContent='Weryfikuję…';box.textContent='Sprawdzanie SHA-256 i struktury kopii. Żadne polecenie nie zostanie wysłane do switcha…';try{const response=await fetch('/api/config/backups/restore-preview?id='+encodeURIComponent(id),{cache:'no-store'});if(!response.ok)throw new Error('Nie można zweryfikować kopii: HTTP '+response.status);const d=await response.json();box.textContent='Kopia '+d.backupId+' · integralność SHA-256: potwierdzona · '+d.lineCount+' niepustych linii · '+fmtCount(d.size)+' B.\n'+d.message+'\nNie wykonano żadnej zmiany na switchu.';}catch(e){box.textContent=e.message;}finally{button.disabled=false;button.textContent='Sprawdź kopię';}});$('refresh-config-backups').onclick=refreshConfigBackups;
$('create-config-backup').onclick=async()=>{const b=$('create-config-backup');if(!confirm('Pobrać running-config i zapisać lokalną kopię na serwerze panelu? Plik może zawierać hasła i inne dane poufne.'))return;b.disabled=true;try{const r=await fetch('/api/config/backups',{method:'POST'});if(!r.ok)throw new Error('HTTP '+r.status);const d=await r.json();$('config-diff').textContent='Utworzono kopię '+d.id+'\nSHA256: '+d.sha256;await refreshConfigBackups();}catch(e){$('config-diff').textContent='Nie udało się utworzyć kopii: '+e.message;}finally{b.disabled=false;}};
$('compare-config').onclick=async()=>{const box=$('config-diff'),button=$('compare-config');button.disabled=true;box.textContent='Pobieranie i porównywanie konfiguracji…';try{const response=await fetch('/api/config/compare');if(!response.ok)throw new Error('Błąd porównania: HTTP '+response.status);const d=await response.json();const added=Number(d.added)||0,removed=Number(d.removed)||0;box.textContent='Różnice: '+added+' dodanych w running-config · '+removed+' usuniętych względem startup-config · Linie: running '+d.runningLines+' / startup '+d.startupLines+'. '+(added===0&&removed===0?'Konfiguracje są zgodne.':'Szczegółowe linie pominięto, ponieważ mogą zawierać dane poufne.');}catch(e){box.textContent=e.message;}finally{button.disabled=false;}};
$('save-config').onclick=async()=>{if(!confirm('Zapisać running-config do startup-config?'))return;try{const d=await api('/api/save',{});showFriendlyResult('output',d.output,'Zapis konfiguracji');alert('Polecenie zapisu wysłane.');}catch(x){alert(x.message);}};
$('run').onclick=async()=>{const command=$('cmd').value;$('output').textContent='Łączenie przez SSH…';try{const d=await api('/api/command',{command});showFriendlyResult('output',d.output,'Diagnostyka urządzenia');}catch(x){showFriendlyResult('output',x.message,'Operacja na switchu');}};async function refreshPortProtection(){
 const el=$('port-protection-status');if(!el)return;
 try{
  const data=await api('/api/ports/protection');
  if(data.configured&&Array.isArray(data.ports)&&data.ports.length){
   el.classList.remove('is-warning');
   el.textContent='Ochrona aktywna dla '+data.count+' portów: '+data.ports.join(', ')+'. Lista pochodzi z PROTECTED_PORTS na serwerze.';
  }else{
   el.classList.add('is-warning');
   el.textContent='UWAGA: nie skonfigurowano chronionych portów. Panel nie wykrywa automatycznie portu zarządzającego — ustaw PROTECTED_PORTS w pliku .env przed wykonywaniem zmian masowych.';
  }
 }catch(e){
  el.classList.add('is-warning');
  el.textContent='Nie można potwierdzić ochrony portów: '+e.message+'. Do czasu sprawdzenia konfiguracji unikaj zmian na uplinkach i porcie zarządzającym.';
 }
}
load();setInterval(()=>{if(document.visibilityState==='visible')load();},15000);refreshPortProtection();refreshSystem();setInterval(()=>{if(document.visibilityState==='visible')refreshSystem();},60000);restoreMonitorHistory();connectMonitorStream();refreshConfigBackups();refreshAudit();
async function loadPanelUsers(){
 const body=$('panel-users-rows'),status=$('panel-users-status');if(!body)return;
 try{
  const data=await api('/api/users');
  const users=Array.isArray(data.users)?data.users:[];
  body.innerHTML=users.map(user=>'<tr data-panel-user="'+esc(user.id)+'"><td><b>'+esc(user.username)+'</b><small class="muted">'+(user.id===undefined?'':'Konto panelu')+'</small></td><td><select data-user-role aria-label="Rola użytkownika '+esc(user.username)+'"><option value="viewer" '+(user.role==='viewer'?'selected':'')+'>Podgląd</option><option value="operator" '+(user.role==='operator'?'selected':'')+'>Operator</option><option value="admin" '+(user.role==='admin'?'selected':'')+'>Administrator</option></select></td><td><label class="inline-check"><input type="checkbox" data-user-active '+(user.active?'checked':'')+'> Aktywne</label></td><td><input type="password" data-user-password autocomplete="new-password" minlength="12" placeholder="Nowe hasło (opcjonalnie)" aria-label="Nowe hasło dla '+esc(user.username)+'"></td><td><button class="btn primary" type="button" data-user-save>Zapisz</button></td></tr>').join('')||'<tr><td colspan="5" class="empty">Brak kont użytkowników.</td></tr>';
  status.textContent='Liczba kont: '+users.length+'. Hasła nie są widoczne w panelu.';
 }catch(e){status.textContent='Nie udało się pobrać listy kont: '+e.message;body.innerHTML='<tr><td colspan="5" class="empty">Błąd odczytu listy użytkowników.</td></tr>';}
}
$('users-refresh')?.addEventListener('click',loadPanelUsers);
$('panel-user-create')?.addEventListener('click',async()=>{
 const username=$('panel-user-name').value.trim(),password=$('panel-user-password').value,role=$('panel-user-role').value,button=$('panel-user-create');
 if(!username||!password){$('panel-users-status').textContent='Podaj login i hasło początkowe.';return;}
 button.disabled=true;
 try{await api('/api/users',{username,password,role});$('panel-user-name').value='';$('panel-user-password').value='';$('panel-users-status').textContent='Konto zostało utworzone.';await loadPanelUsers();}
 catch(e){$('panel-users-status').textContent='Nie udało się utworzyć konta: '+e.message;}
 finally{button.disabled=false;}
});
$('panel-users-rows')?.addEventListener('click',async event=>{
 const button=event.target.closest('[data-user-save]');if(!button)return;
 const row=button.closest('[data-panel-user]');if(!row)return;
 const payload={role:row.querySelector('[data-user-role]').value,active:row.querySelector('[data-user-active]').checked};
 const password=row.querySelector('[data-user-password]').value;if(password)payload.password=password;
 button.disabled=true;
 try{await api('/api/users/'+encodeURIComponent(row.dataset.panelUser),payload,'PATCH');row.querySelector('[data-user-password]').value='';$('panel-users-status').textContent='Zapisano zmiany konta.';await loadPanelUsers();}
 catch(e){$('panel-users-status').textContent='Nie udało się zapisać konta: '+e.message;}
 finally{button.disabled=false;}
});
async function loadSessionIdentity(){const el=$('session-identity');if(!el)return;try{const d=await api('/api/session');const labels={viewer:'Podgląd',operator:'Operator',admin:'Administrator'};el.textContent=(d.username||'Konto')+' · '+(labels[d.role]||d.role||'');el.title='Zalogowano jako '+(d.username||'konto')+' · rola: '+(d.role||'nieznana');document.body.dataset.role=d.role||'';const panel=document.getElementById('panel-users'),nav=document.getElementById('panel-users-nav');if(panel)panel.hidden=d.role!=='admin';if(nav)nav.hidden=d.role!=='admin';if(d.role==='admin')loadPanelUsers();}catch(_e){el.textContent='Sesja';}}
loadSessionIdentity();
$('logout').onclick=async()=>{try{await fetch('/api/logout',{method:'POST',credentials:'same-origin'});}finally{location.href='/login';}};


// Failure diagnostics center: manual, read-only collection with persisted comparisons.
function renderDiagnosticsCenter(data){
 const alerts=$('diagnostics-alerts'),transitions=$('diagnostics-transitions'),samples=$('diagnostics-samples'),meta=$('diagnostics-meta');
 if(!alerts||!transitions||!samples||!meta)return;
 const list=Array.isArray(data.samples)?data.samples:[];
 const latest=list[list.length-1];
 meta.textContent='Próbek w historii: '+(data.sampleCount??list.length)+' · Ostatni odczyt: '+(latest?new Date(latest.capturedAt).toLocaleString():'brak')+' · '+(data.hasEnoughHistory?'porównanie aktywne':'za mało historii do wykrywania zmian')+' · próbkowanie serwera: '+(data.collectionMode==='scheduled'?'automatyczne co '+(data.collectionInterval||'60s'):'wyłączone — tylko ręczne')+' · flapping od 3 zmian';
 const severityLabel={critical:'KRYTYCZNY',warning:'OSTRZEŻENIE',info:'INFORMACJA'};
 alerts.innerHTML=(data.alerts||[]).length?(data.alerts||[]).map(a=>'<article class="diagnostic-alert severity-'+esc(a.severity)+'"><span>'+esc(severityLabel[a.severity]||a.severity)+'</span><div class="diagnostic-alert-body"><b>'+esc(a.title)+'</b><small>'+esc(a.evidence)+'</small>'+(a.acknowledged?'<div class="diagnostic-acknowledged">Potwierdził: '+esc(a.acknowledgement?.username||'operator')+' · '+esc(new Date(a.acknowledgement?.acknowledgedAt||a.at).toLocaleString())+(a.acknowledgement?.comment?'<small>'+esc(a.acknowledgement.comment)+'</small>':'')+'</div>':(a.code==='lacp-flapping'?'<div class="diagnostic-acknowledged">Alarm wyliczany z okna czasowego. Zniknie po ustaniu powtarzających się zmian.</div>':'<div class="diagnostic-ack-form"><input type="text" maxlength="500" placeholder="Komentarz operatora (opcjonalnie)" data-ack-comment="'+esc(a.id)+'"><button class="btn small" type="button" data-ack-alert="'+esc(a.id)+'">Potwierdź alarm</button></div>'))+'</div></article>').join(''):'<div class="empty">Brak rozpoznanych bieżących alarmów. To nie jest certyfikat poprawności całej sieci.</div>';
 transitions.innerHTML=(data.transitions||[]).length?(data.transitions||[]).slice().reverse().map(t=>'<article class="diagnostic-transition"><span class="severity-dot severity-'+esc(t.severity)+'"></span><div><b>'+esc(t.subject)+'</b><small>'+esc(t.from)+' → '+esc(t.to)+'</small></div><time>'+esc(new Date(t.at).toLocaleString())+'</time></article>').join(''):'<div class="empty">'+esc(data.note||'Brak zmian między porównywalnymi próbkami.')+'</div>';
 samples.innerHTML=list.length?list.slice().reverse().slice(0,8).map(s=>'<article class="diagnostic-sample"><b>'+esc(new Date(s.capturedAt).toLocaleString())+'</b><small>Kontrole: '+Object.entries(s.checks||{}).filter(([,v])=>v).map(([k])=>({runningConfig:'konfiguracja',switchport:'switchport',lldp:'LLDP',lacp:'LACP'}[k]||k)).join(' · ')+' </small><small>LACP: '+(s.aggregations||[]).length+' · uplinki: '+(s.uplinks||[]).length+' · sąsiedzi: '+(s.neighbors||[]).length+'</small></article>').join(''):'<div class="empty">Brak zapisanych próbek.</div>';
}


function renderAvailabilityMetric(metric){
 const percent=metric.availabilityPercent===null||metric.availabilityPercent===undefined?'—':Number(metric.availabilityPercent).toFixed(2)+'%';
 const observed=Number(metric.observedSeconds||0),unknown=Number(metric.unknownSeconds||0),outages=Number(metric.outageCount||0);
 const duration=s=>{s=Math.max(0,Number(s)||0);if(s<60)return Math.round(s)+' s';if(s<3600)return Math.round(s/60)+' min';if(s<86400)return (s/3600).toFixed(1)+' h';return (s/86400).toFixed(1)+' d';};
 return '<article class="availability-card"><span>'+esc(metric.name)+'</span><strong>'+percent+'</strong><small>Pomiar: '+duration(observed)+' · Brak danych: '+duration(unknown)+'</small><small>Przerwy: '+outages+' · Łącznie: '+duration(metric.outageSeconds)+'</small></article>';
}
function renderAvailabilityChart(points,window){
 const chart=$('availability-chart');if(!chart)return;
 chart.innerHTML=(points||[]).map((p,i)=>{
  const value=p.availabilityPercent===null||p.availabilityPercent===undefined?null:Number(p.availabilityPercent);
  const height=value===null?0:Math.max(2,Math.min(100,value));
  const date=new Date(p.at);
  const label=window==='7d'?date.toLocaleDateString(undefined,{day:'2-digit',month:'2-digit'}):(i%3===0?date.toLocaleTimeString(undefined,{hour:'2-digit',minute:'2-digit'}):'');
  const observed=Math.max(0,Number(p.observedSeconds||0)),unknown=Math.max(0,Number(p.unknownSeconds||0));
  const title=(value===null?'Brak wystarczających danych':value.toFixed(2)+'% dostępności')+' · pomiar '+Math.round(observed/60)+' min · brak danych '+Math.round(unknown/60)+' min';
  return '<div class="availability-bar-column" title="'+esc(title)+'"><div class="availability-bar-track"><div class="availability-bar '+(value===null?'unknown':'')+'" style="height:'+height+'%"></div></div><small>'+esc(label)+'</small><span>'+(value===null?'—':value.toFixed(0)+'%')+'</span></div>';
 }).join('');
 const caption=$('availability-chart-caption');if(caption)caption.textContent=window==='7d'?'Ostatnie 7 dni · podział dzienny':'Ostatnie 24 godziny · podział godzinowy';
}
async function refreshAvailability(){
 const cards=$('availability-cards'),groups=$('availability-aggregations'),note=$('availability-note');if(!cards||!groups)return;
 try{
  const window=$('availability-window')?.value||'24h';const data=await api('/api/diagnostics/availability?window='+window);
  cards.innerHTML=renderAvailabilityMetric(data.device);
  groups.innerHTML=(data.aggregations||[]).length?'<h5>Dostępność agregacji LACP</h5><div class="availability-cards">'+data.aggregations.map(renderAvailabilityMetric).join('')+'</div>':'<div class="empty">Brak rozpoznanych agregacji LACP w historii.</div>';
  renderAvailabilityChart(data.timeline||[],window);
  if(note)note.textContent=data.method+' Maksymalna luka między próbkami: '+data.maxSampleGapSeconds+' s.';
 }catch(e){cards.innerHTML='<div class="empty">Nie udało się pobrać dostępności: '+esc(e.message)+'</div>';}
}

function renderNetworkEvents(data){
 const root=$('events-timeline'),meta=$('events-meta');if(!root||!meta)return;
 const labels={alarm:'ALARM',lacp:'LACP',lldp:'LLDP',availability:'KONTROLA CLI'};
 const severity={critical:'KRYTYCZNY',warning:'OSTRZEŻENIE',info:'INFORMACJA'};
 const rows=Array.isArray(data.events)?data.events:[];
 const correlations=Array.isArray(data.correlations)?data.correlations:[];
 const correlationRoot=$('events-correlations');
 if(correlationRoot)correlationRoot.innerHTML=correlations.length?correlations.map(x=>'<article class="event-correlation severity-'+esc(x.severity)+'"><div><b>'+esc(x.title)+'</b><small>'+esc(x.summary)+'</small><span>'+esc(x.categories.join(' · ').toUpperCase())+' · '+esc(x.eventCount)+' zdarzenia · '+esc(new Date(x.start).toLocaleTimeString())+'–'+esc(new Date(x.end).toLocaleTimeString())+'</span></div></article>').join(''):'<div class="empty compact">Brak wykrytych korelacji w pięciominutowym oknie. Korelacja nie oznacza potwierdzonej przyczyny.</div>';
 meta.textContent='Zdarzenia: '+(data.total??rows.length)+' · okres: '+data.window+' · od najnowszych';
 root.innerHTML=rows.length?rows.map(e=>'<article class="network-event severity-'+esc(e.severity)+'"><span class="event-kind">'+esc(labels[e.type]||e.type)+'</span><span class="severity-tag">'+esc(severity[e.severity]||e.severity)+'</span><div class="network-event-body"><b>'+esc(e.title)+'</b><strong>'+esc(e.subject)+'</strong><small>'+esc(e.description)+'</small>'+(e.acknowledged?'<div class="diagnostic-acknowledged">Potwierdził: '+esc(e.acknowledgement?.username||'operator')+' · '+esc(new Date(e.acknowledgement?.acknowledgedAt||e.at).toLocaleString())+(e.acknowledgement?.comment?'<small>'+esc(e.acknowledgement.comment)+'</small>':'')+'</div>':'')+'</div><time>'+esc(new Date(e.at).toLocaleString())+'</time></article>').join(''):'<div class="empty">Brak zdarzeń w wybranym okresie i kategorii.</div>';
}
async function refreshNetworkEvents(){
 const root=$('events-timeline');if(!root)return;
 const window=$('events-window')?.value||'24h',type=$('events-type')?.value||'all';
 try{const data=await api('/api/events?window='+encodeURIComponent(window)+'&type='+encodeURIComponent(type)+'&limit=200');renderNetworkEvents(data);}
 catch(e){root.innerHTML='<div class="empty">Nie udało się pobrać zdarzeń: '+esc(e.message)+'</div>';}
}
async function refreshDiagnosticsCenter(){
 const meta=$('diagnostics-meta');if(meta)meta.textContent='Odczyt historii diagnostycznej…';
 try{const windowMinutes=Number($('diagnostics-window')?.value||60);const data=await api('/api/diagnostics/history?limit=100&windowMinutes='+windowMinutes);renderDiagnosticsCenter(data);}
 catch(e){if(meta)meta.textContent='Nie udało się odczytać historii: '+e.message;}
}
async function collectDiagnosticsCenter(){
 const button=$('collect-diagnostics');if(!button)return;
 button.disabled=true;button.textContent='Zbieranie…';
 try{const result=await api('/api/diagnostics/collect',{});await refreshDiagnosticsCenter();if($('diagnostics-meta'))$('diagnostics-meta').textContent='Próbka zapisana: '+new Date(result.sample.capturedAt).toLocaleString()+' · '+Object.values(result.sample.checks||{}).filter(Boolean).length+' / 4 kontroli dostępnych.';}
 catch(e){if($('diagnostics-meta'))$('diagnostics-meta').textContent='Odczyt próbki nie powiódł się: '+e.message+' (jeśli próbka została zapisana, historia odświeży się ponownie).';await refreshDiagnosticsCenter();await refreshAvailability();}
 finally{button.disabled=false;button.textContent='Zbierz próbkę';}
}
$('refresh-events')?.addEventListener('click',refreshNetworkEvents);$('events-window')?.addEventListener('change',refreshNetworkEvents);$('events-type')?.addEventListener('change',refreshNetworkEvents);refreshNetworkEvents();
if($('collect-diagnostics')){$('collect-diagnostics').addEventListener('click',collectDiagnosticsCenter);$('refresh-diagnostics')?.addEventListener('click',refreshDiagnosticsCenter);$('diagnostics-window')?.addEventListener('change',refreshDiagnosticsCenter);refreshDiagnosticsCenter();setInterval(()=>{if(!document.hidden){refreshDiagnosticsCenter();refreshAvailability();refreshNetworkEvents();}},30000);$('availability-window')?.addEventListener('change',refreshAvailability);refreshAvailability();$('export-availability')?.addEventListener('click',()=>{const window=$('availability-window')?.value||'24h';location.href='/api/diagnostics/availability.csv?window='+window;});}

async function acknowledgeDiagnosticsAlert(id,button){
 const input=$('diagnostics-alerts')?.querySelector('[data-ack-comment="'+id+'"]');
 button.disabled=true;button.textContent='Zapisywanie…';
 try{await api('/api/diagnostics/acknowledge',{id,comment:input?.value||''});await refreshDiagnosticsCenter();}
 catch(e){button.disabled=false;button.textContent='Potwierdź alarm';const meta=$('diagnostics-meta');if(meta)meta.textContent='Nie udało się potwierdzić alarmu: '+e.message;}
}
$('diagnostics-alerts')?.addEventListener('click',event=>{
 const button=event.target.closest('[data-ack-alert]');if(button)acknowledgeDiagnosticsAlert(button.dataset.ackAlert,button);
});
