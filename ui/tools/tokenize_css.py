"""Jednorazowa migracja starego web/style.css do plików web/css/features/*.css:
- usuwa omyłkowy prefiks :root[data-theme="light"] (708 reguł działało tylko w jasnym motywie),
- zamienia zakodowane kolory na tokeny według roli właściwości i odcienia,
- pomija reguły elementów przepisanych od nowa (base/components/layout/overview/faceplate),
- rozdziela resztę tematycznie."""
import re, colorsys, collections, tinycss2
SRC='/home/claude/work/orig_style.css'; OUT='/home/claude/ciscoswitch/web/css/features/'
LIGHT=re.compile(r'(?::root)?\[data-theme="light"\]\s*')
OWNED=re.compile(r'''^(
  html|body|\*(::?[a-z-]+)?|button|input|select|textarea|pre|table|th|td|thead.*|tbody\s.*|code|kbd|h[1-6]|a|footer.*|
  \.(shell|sidebar|brand|side-label|side-bottom|dot|online|offline|topbar|crumb|top-actions|btn|welcome|device-|chip-status|
     metrics|metric(-[a-z]+)?|section-head|table-wrap|table-tools|state|empty|note|alert|hidden|form-grid|form-actions|setting-row|
     fp-|faceplate|port-dialog|dialog-|session-identity|eyebrow|wide|cli-row|edge-select|table-foot|live-label|
     nav-search|sidebar-nav|nav-group|nav-icon|nav\b|main\b|panel\b(?!-))[\w-]*.*|
  \.switch(\s.*)?|\.switch\s*(input|span).*|:root
)$''', re.X)
HEX=re.compile(r'#([0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{4}|[0-9a-fA-F]{3})\b')
RGB=re.compile(r'rgba?\(([^)]*)\)')
def parse(c):
    if c.startswith('#'):
        h=c[1:]
        if len(h) in (3,4): h=''.join(x*2 for x in h)
        r,g,b=(int(h[i:i+2],16)/255 for i in (0,2,4)); a=int(h[6:8],16)/255 if len(h)==8 else 1
        return r,g,b,a
    p=[x.strip() for x in re.split(r'[,\s/]+',c[c.index('(')+1:-1]) if x.strip()]
    r,g,b=(float(x)/255 for x in p[:3]); a=float(p[3].rstrip('%'))/(100 if p[3].endswith('%') else 1) if len(p)>3 else 1
    return r,g,b,a
def family(r,g,b):
    mx,mn=max(r,g,b),min(r,g,b); C=mx-mn; L=(mx+mn)/2
    h=colorsys.rgb_to_hls(r,g,b)[0]*360
    S=colorsys.rgb_to_hls(r,g,b)[2]
    if C<0.035 or C<0.07 or (185<=h<=240 and S<0.55): return None,L
    s='red' if h<15 or h>=330 else 'amber' if h<65 else 'green' if h<175 else 'blue' if h<260 else 'violet'
    return s,L
def role(prop):
    if prop in('color','caret-color','-webkit-text-fill-color','fill'): return 'text'
    if prop.startswith(('border','outline','column-rule','stroke')): return 'line'
    if prop in('box-shadow','text-shadow'): return 'shadow'
    return 'bg'
stats=collections.Counter(); seen=collections.Counter()
def token(prop,c):
    r,g,b,a=parse(c); s,L=family(r,g,b); rl=role(prop); stats[rl]+=1; seen[(s,rl,c.lower())]+=1
    if s:
        if rl=='text': return f'var(--{s}-fg)'
        if rl=='line': return f'var(--{s}-line)' if a<.6 or L<.3 or L>.85 else f'var(--{s})'
        if rl=='shadow': return f'var(--{s}-soft)'
        return f'var(--{s}-soft)' if a<.5 or L<.3 or L>.85 else f'var(--{s})'
    if rl=='shadow': return 'var(--shadow-color)'
    if a<.2 and rl=='bg': return 'transparent' if a<.05 else 'var(--raised)'
    dark= L<0.5
    if rl=='text':
        if dark: return 'var(--text)' if L<.2 else 'var(--text-2)' if L<.33 else 'var(--muted)'
        return 'var(--text)' if L>=.85 else 'var(--text-2)' if L>=.72 else 'var(--muted)' if L>=.58 else 'var(--subtle)'
    if rl=='line':
        if dark: return 'var(--line-soft)' if L<.17 else 'var(--line)' if L<.27 else 'var(--line-strong)'
        return 'var(--line-soft)' if L>=.92 else 'var(--line)' if L>=.85 else 'var(--line-strong)'
    if dark: return 'var(--sunken)' if L<.1 else 'var(--surface)' if L<.135 else 'var(--raised)' if L<.19 else 'var(--line-strong)' if L<.35 else 'var(--subtle)'
    return 'var(--surface)' if L>=.985 else 'var(--sunken)' if L>=.955 else 'var(--raised)' if L>=.9 else 'var(--line-strong)'
NAMED={'white':'#ffffff','black':'#000000'}
def tokenize_value(prop,val):
    val=re.sub(r'\b(white|black)\b',lambda m:NAMED[m.group(1)],val)
    val=RGB.sub(lambda m:token(prop,m.group(0)),val)
    val=HEX.sub(lambda m:token(prop,m.group(0)),val)
    g=re.fullmatch(r'\s*(?:repeating-)?linear-gradient\((?:[^,()]+,)?\s*(var\(--[\w-]+\))(?:\s+\d+%?)?(?:\s*,\s*\1(?:\s+[\d.]+(?:%|px)?)*)*\s*\)\s*',val)
    if g: val=g.group(1)
    if prop=='background' and 'radial-gradient' in val: val='var(--bg)'
    return val
def rule_text(prelude,content):
    sels=[LIGHT.sub('',s).strip() for s in re.sub(r'/\*.*?\*/','',prelude,flags=re.S).split(',')]
    keep=[s for s in sels if s and not OWNED.match(s)]
    if not keep: return None,None
    decls=[]
    for d in tinycss2.parse_blocks_contents(content,skip_whitespace=True,skip_comments=True):
        if d.type!='declaration': continue
        v=tinycss2.serialize(d.value).strip()
        decls.append(f'{d.name}:{tokenize_value(d.lower_name,v)}'+('!important' if d.important else ''))
    if not decls: return None,None
    return ','.join(keep), ';'.join(decls)
BUCKETS=[('ports',r'port-|quality|compare|\baudit|profile|bulk|compliance|repair|counter|traffic-meter|meter-|detail|diagnostic-(summary|live|tables)|network-context|mirror|role-audit|top-ports'),
 ('switching',r'vlan|lacp|stp|\btrf|storm|loop|igmp|qos|edge'),
 ('network',r'topology|l3-|dhcp-server|route|probe|arp|neighbor|lldp'),
 ('diagnostics',r'diagnostics|availability|events|alert-|monitor-events|event-|correlation|diag-'),
 ('system',r'system-|snmp|ssh|config-|backup|services|svc-|files|fw-|ntp|time-|tz-|security|\.ps-|acl|users|user-|role-permissions|sys-|services-panel'),
 ('tools',r'compat|\.set-|settings|notify|\.mon-|log|toast|loading'),
 ('dashboard',r'dashboard|switch-device|switch-led|switch-panel|map-|legend|traffic-|health|quick-|rx-dot|tx-dot|event-row')]
def bucket(sel):
    for name,pat in BUCKETS:
        if re.search(pat,sel): return name
    return 'misc'
out=collections.defaultdict(list); dropped=0; kept=0
def walk(nodes,media=None):
    global dropped,kept
    for n in nodes:
        if n.type=='qualified-rule':
            sel,body=rule_text(tinycss2.serialize(n.prelude),n.content)
            if sel is None: dropped+=1; continue
            kept+=1; out[bucket(sel)].append((media,f'{sel}{{{body}}}'))
        elif n.type=='at-rule' and n.lower_at_keyword=='media':
            q=tinycss2.serialize(n.prelude).strip()
            if 'reduced-motion' in q: continue
            walk(tinycss2.parse_rule_list(n.content,skip_whitespace=True,skip_comments=True),q)
rules=tinycss2.parse_stylesheet(open(SRC,encoding='utf-8').read(),skip_whitespace=True,skip_comments=True)
walk(rules)
HEAD="/* Wygenerowane z dawnego style.css przez tools/tokenize_css.py, potem utrzymywane ręcznie.\n   Kolory to wyłącznie tokeny z tokens.css; reguły działają w obu motywach. */\n"
for name,items in out.items():
    lines=[HEAD]; cur=None; buf=[]
    def flush():
        if buf: lines.append(f'@media {cur}{{\n'+'\n'.join(buf)+'\n}' if cur else '\n'.join(buf))
    for media,txt in items:
        if media!=cur: flush(); cur=media; buf=[]
        buf.append(txt)
    flush()
    open(OUT+name+'.css','w').write('\n'.join(lines)+'\n')
print('zachowane',kept,'pominięte (przepisane od nowa)',dropped,'| zamienione kolory wg roli',dict(stats))
for name,items in sorted(out.items()): print(f'  {name}.css: {len(items)} reguł')

import sys
for (s_,rl,c),n in sorted(seen.items(),key=lambda x:(str(x[0][0]),x[0][1])):
    if s_=='blue': print('   niebieski',rl,c,n)
