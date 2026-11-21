"""Jednorazowa migracja (już wykonana): pocięła dawny web/index.html na komponenty gtml w ui/components.
import re, json, os, sys
SRC = '/home/claude/work/orig_index.html'
UI = '/home/claude/ciscoswitch/ui/components'
src = open(SRC, encoding='utf-8').read()
kids = json.load(open('/tmp/kids.json'))

ACRO = {'ARP','AT-GS948MX','CLI','DNS','FDB','HTTP','ID','IF-MIB','IP','LACP','LLDP','MAC','MSTP',
        'MTU','RSTP','RX','SNMP','STP','TX','UTC','VLAN','SFP','SFP+','CPU','UP','DOWN','QOS','SSH','DHCP'}
SPECIAL = {'LIVE':'Na żywo','SYSOBJECTID':'sysObjectID','PORT A':'Port A','PORT B':'Port B',
           'OBciążenie CPU':'Obciążenie CPU','TOP 10':'Top 10'}

def sentence(text):
    core = text.strip()
    if core in SPECIAL: return text.replace(core, SPECIAL[core])
    letters = [c for c in core if c.isalpha()]
    if len(letters) < 2 or not all(c.isupper() for c in letters): return text
    out, first = [], True
    for tok in re.split(r'(\s+|/|·|—|,|\(|\))', core):
        if not tok or not re.search(r'[A-Za-zĄĆĘŁŃÓŚŹŻ]', tok): out.append(tok); continue
        if tok in ACRO or re.fullmatch(r'UTC[+-]\d+', tok): out.append(tok)
        else:
            low = tok.lower()
            out.append(low[0].upper() + low[1:] if first else low)
        first = False
    return text.replace(core, ''.join(out))

def transform(html):
    html = re.sub(r'<div class="eyebrow">[^<]*</div>', '', html)
    html = re.sub(r'>([^<>]+)<', lambda m: '>' + sentence(m.group(1)) + '<', html)
    for a, b in [('>VLAN Manager<', '>VLAN-y<'), ('>Configuration Manager<', '>Konfiguracja i kopie<'),
                 ('>DHCP Server<', '>Serwer DHCP<'), ('RX (niebieski) · TX (fioletowy) · ostatnie 60 próbek', 'ostatnie 60 próbek'),
                 ('48 × 1G · 2 combo · 2 × 10G SFP+', '48 × 1G, 2 combo, 2 × SFP+ 10G'),
                 ('Grupowanie w oknie 5 minut · hipoteza czasowa, nie potwierdzona przyczyna', 'Grupowanie w oknie 5 minut. To hipoteza czasowa, nie potwierdzona przyczyna.')]:
        html = html.replace(a, b)
    return html

NAMES = {  # (id lub indeks) -> (katalog, komponent); None = pisane ręcznie
 'topbar':None, 'overview':None, 'warning':('overview','WarningBanner'), 'metrics':None,
 'network-dashboard':('overview','DashboardGrid'), 'monitoring':('ports','TrafficAnalysis'),
 'port-quality':('ports','PortQuality'), 'port-history':('ports','PortHistory'),
 'counter-history':('ports','ErrorHistory'), 'ports-section':('ports','Ports'),
 'vlan-port-explorer':('switching','VlanMap'), 'topology':('network','Topology'),
 'vlans':('switching','VlanManager'), 'port-dialog':('ports','PortDialog'),
 'lower#1':('switching','LacpStp'), 'lower#2':('system','DhcpAndDiagnostics'),
 'configuration-manager':('system','ConfigManager'), 'system-health':('system','SystemHealth'),
 'switch-logs':('tools','SwitchLogs'), 'time-ntp':('system','TimeNtp'), 'mirroring':('ports','Mirroring'),
 'traffic-protection':('switching','TrafficProtection'), 'network-security':('system','NetworkSecurity'),
 'compat-report':('tools','CompatReport'), 'panel-settings':('tools','PanelSettings'), 'qos':('switching','Qos'),
 'notifications':('tools','Notifications'), 'files':('system','Files'), 'services':('system','Services'),
 'ssh':('system','Ssh'), 'panel-users':('tools','PanelUsers'), 'snmp':('system','Snmp'),
 'routing':('network','Routing'), 'dhcp-server':('network','DhcpServer'), 'audit':('tools','AuditLog'),
 'footer':None }
order, lower = [], 0
for t, a, st, e in kids:
    key = a.get('id') or a.get('class') or t
    if key == 'lower-grid': lower += 1; key = f'lower#{lower}'
    if t == 'footer': key = 'footer'
    chunk = src[st:e]
    assert chunk.startswith('<' + t) and chunk.endswith('</' + t + '>'), (key, chunk[:40], chunk[-40:])
    target = NAMES[key]
    if target is None: order.append(('manual', key)); continue
    d, name = target
    os.makedirs(f'{UI}/{d}', exist_ok=True)
    open(f'{UI}/{d}/{name}.html', 'w', encoding='utf-8').write(transform(chunk) + '\n')
    order.append((d, name))
json.dump(order, open('/home/claude/work/order.json', 'w'))
print('\n'.join(f'{d}/{n}' for d, n in order))
