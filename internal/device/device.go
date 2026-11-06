package device

import (
 "fmt"
 "net"
 "regexp"
 "sort"
 "strconv"
 "strings"
 "time"

 "github.com/gosnmp/gosnmp"
 "golang.org/x/crypto/ssh"
)

type Config struct { Host, SSHPort, SSHUser, SSHPassword string; SNMPCommunity, SNMPPort string; Timeout time.Duration }
type Service struct { cfg Config }
type Port struct {
 Index int `json:"index"`
 Name string `json:"name"`
 Admin string `json:"admin"`
 Oper string `json:"oper"`
 Speed string `json:"speed"`
 MTU uint32 `json:"mtu"`
 InOctets uint64 `json:"inOctets"`
 OutOctets uint64 `json:"outOctets"`
 InErrors uint64 `json:"inErrors"`
 OutErrors uint64 `json:"outErrors"`
 InDiscards uint64 `json:"inDiscards"`
 OutDiscards uint64 `json:"outDiscards"`
 InUcastPkts uint64 `json:"inUcastPkts"`
 OutUcastPkts uint64 `json:"outUcastPkts"`
}
type Status struct {
 Host string `json:"host"`
 Reachable bool `json:"reachable"`
 Ports []Port `json:"ports"`
 Warning string `json:"warning,omitempty"`
}

func New(c Config) *Service { return &Service{cfg:c} }

func (s *Service) Status() Status {
 o:=Status{Host:s.cfg.Host,Ports:[]Port{}}
 if s.cfg.Host=="" || s.cfg.SNMPCommunity=="" { o.Warning="Brak SWITCH_HOST lub SWITCH_SNMP_COMMUNITY. Sprawdź .env i uruchom panel ponownie."; return o }
 g:=*gosnmp.Default
 g.Target=s.cfg.Host
 g.Port=uint16(port(s.cfg.SNMPPort,161))
 g.Community=s.cfg.SNMPCommunity
 g.Version=gosnmp.Version2c
 g.Timeout=s.cfg.Timeout
 g.Retries=1
 if e:=g.Connect();e!=nil { o.Warning="SNMP connect: "+e.Error(); return o }
 defer g.Conn.Close()
 d,e:=walkText(&g,".1.3.6.1.2.1.2.2.1.2")
 if e!=nil { o.Warning="IF-MIB ifDescr: "+e.Error(); return o }
 if len(d)==0 { o.Warning="SNMP działa, ale IF-MIB ifDescr jest pusty."; return o }
 o.Reachable=true
 mtu,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.4")
 speed,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.5")
 highSpeed,_:=walkNum(&g,".1.3.6.1.2.1.31.1.1.1.15")
 adm,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.7")
 op,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.8")
 inOctets,_:=walkCounter(&g,".1.3.6.1.2.1.31.1.1.1.6")
 outOctets,_:=walkCounter(&g,".1.3.6.1.2.1.31.1.1.1.10")
 inErrors,_:=walkCounter(&g,".1.3.6.1.2.1.2.2.1.14")
 outErrors,_:=walkCounter(&g,".1.3.6.1.2.1.2.2.1.20")
 inDiscards,_:=walkCounter(&g,".1.3.6.1.2.1.2.2.1.13")
 outDiscards,_:=walkCounter(&g,".1.3.6.1.2.1.2.2.1.19")
 inUcast,_:=walkCounter(&g,".1.3.6.1.2.1.31.1.1.1.7")
 outUcast,_:=walkCounter(&g,".1.3.6.1.2.1.31.1.1.1.11")
 ix:=make([]int,0,len(d))
 for i:=range d { ix=append(ix,i) }
 sort.Ints(ix)
 for _,i:=range ix {
  p:=Port{Index:i,Name:d[i],Admin:state(adm[i]),Oper:state(op[i]),MTU:mtu[i],InOctets:inOctets[i],OutOctets:outOctets[i],InErrors:inErrors[i],OutErrors:outErrors[i],InDiscards:inDiscards[i],OutDiscards:outDiscards[i],InUcastPkts:inUcast[i],OutUcastPkts:outUcast[i]}
  if highSpeed[i]>0 { p.Speed=fmt.Sprintf("%d Mb/s",highSpeed[i]) } else if speed[i]>0 { p.Speed=fmt.Sprintf("%d Mb/s",speed[i]/1000000) }
  o.Ports=append(o.Ports,p)
 }
 return o
}

func walkText(g *gosnmp.GoSNMP,oid string)(map[int]string,error) {
 m:=map[int]string{}
 e:=g.Walk(oid,func(p gosnmp.SnmpPDU)error {
  a:=strings.Split(p.Name,".")
  i,x:=strconv.Atoi(a[len(a)-1]); if x!=nil{return nil}
  switch v:=p.Value.(type) { case []byte:m[i]=strings.TrimSpace(string(v)); case string:m[i]=strings.TrimSpace(v); default:m[i]=fmt.Sprint(v) }
  return nil
 })
 return m,e
}
func walkNum(g *gosnmp.GoSNMP,oid string)(map[int]uint32,error) {
 m:=map[int]uint32{}
 e:=g.Walk(oid,func(p gosnmp.SnmpPDU)error {
  a:=strings.Split(p.Name,".")
  i,x:=strconv.Atoi(a[len(a)-1]); if x!=nil{return nil}
  switch v:=p.Value.(type) { case uint:m[i]=uint32(v); case uint32:m[i]=v; case int:if v>0{m[i]=uint32(v)}; case int64:if v>0{m[i]=uint32(v)}; case uint64:m[i]=uint32(v) }
  return nil
 })
 return m,e
}
func walkCounter(g *gosnmp.GoSNMP,oid string)(map[int]uint64,error) {
 m:=map[int]uint64{}
 e:=g.Walk(oid,func(p gosnmp.SnmpPDU)error {
  a:=strings.Split(p.Name,".")
  i,x:=strconv.Atoi(a[len(a)-1]); if x!=nil{return nil}
  var n uint64
  switch v:=p.Value.(type) {
  case uint: n=uint64(v)
  case uint32: n=uint64(v)
  case uint64: n=v
  case int: if v>0{n=uint64(v)}
  case int64: if v>0{n=uint64(v)}
  case []byte: n,_=strconv.ParseUint(strings.TrimSpace(string(v)),10,64)
  default: n,_=strconv.ParseUint(fmt.Sprint(v),10,64)
  }
  m[i]=n
  return nil
 })
 return m,e
}
func state(v uint32)string { switch v {case 1:return "up";case 2:return "down";case 3:return "testing";default:return "unknown"} }
func validPort(v string)bool {
 if !strings.HasPrefix(v,"port1.0.") {return false}
 n,e:=strconv.Atoi(strings.TrimPrefix(v,"port1.0."))
 return e==nil&&n>=1&&n<=50
}
func port(v string,f int)int { n,e:=strconv.Atoi(v);if e!=nil||n<1||n>65535{return f};return n }

func (s *Service) runConfig(cmds []string)(string,error) {
 if s.cfg.Host==""||s.cfg.SSHUser==""||s.cfg.SSHPassword=="" {return "",fmt.Errorf("brak konfiguracji SSH w .env")}
 c,e:=ssh.Dial("tcp",net.JoinHostPort(s.cfg.Host,s.cfg.SSHPort),&ssh.ClientConfig{User:s.cfg.SSHUser,Auth:[]ssh.AuthMethod{ssh.Password(s.cfg.SSHPassword)},HostKeyCallback:ssh.InsecureIgnoreHostKey(),HostKeyAlgorithms:[]string{ssh.KeyAlgoRSA},Timeout:s.cfg.Timeout})
 if e!=nil{return "",e}
 defer c.Close()
 ss,e:=c.NewSession();if e!=nil{return "",e};defer ss.Close()
 in,e:=ss.StdinPipe();if e!=nil{return "",e}
 var out strings.Builder
 ss.Stdout=&out;ss.Stderr=&out
 if e=ss.Shell();e!=nil{return "",e}
 for _,x:=range cmds {if _,e=fmt.Fprintln(in,x);e!=nil{return out.String(),e}}
 _=in.Close()
 done:=make(chan error,1);go func(){done<-ss.Wait()}()
 select {
 case e=<-done: if e!=nil{return out.String(),fmt.Errorf("CLI: %w; %s",e,out.String())}
 case <-time.After(8*time.Second): _=ss.Close();return out.String(),fmt.Errorf("timeout CLI")
 }
 return out.String(),nil
}

func (s *Service) ConfigurePort(name,action string)(string,error) {
 if !validPort(name){return "",fmt.Errorf("nieprawidłowy port")}
 cmd:="shutdown"
 if action=="enable"{cmd="no shutdown"}else if action!="disable"{return "",fmt.Errorf("nieobsługiwana operacja")}
 return s.runConfig([]string{"configure terminal","interface "+name,cmd,"end"})
}
func (s *Service) SetManagementDHCP(on bool)(string,error) {
 cmd:="no ip address dhcp";if on{cmd="ip address dhcp"}
 return s.runConfig([]string{"configure terminal","interface vlan1",cmd,"end"})
}
func (s *Service) SaveConfig()(string,error) { return s.runConfig([]string{"copy running-config startup-config"}) }

var allowed=[]string{"show system","show system environment","show version","show cpu","show memory","show interfaces status","show interfaces","show vlan","show vlan all","show ip interface brief","show ip route","show arp","show lldp neighbors","show spanning-tree","show boot","show http","show interface switchport","show running-config interface","show etherchannel","show static-channel-group","show mac address-table","show log","show log tail 30","show system mac","show system serialnumber","show ip dhcp server summary","show ip dhcp binding","show running-config dhcp"}
// SystemInfo returns raw, read-only AlliedWare Plus diagnostics. Values are deliberately
// kept as device output because available sensors and command formats vary by firmware.
func (s *Service) SystemInfo() map[string]string {
 out:=map[string]string{}
 for _,item:=range []struct{key,cmd string}{
  {"version","show version"},
  {"cpu","show cpu"},
  {"memory","show memory"},
  {"environment","show system environment"},
  {"boot","show boot"},
 } {
  value,err:=s.RunReadOnly(item.cmd)
  if err!=nil { out[item.key]="Błąd odczytu: "+err.Error() } else if strings.TrimSpace(value)=="" { out[item.key]="Urządzenie nie zwróciło danych." } else { out[item.key]=strings.TrimSpace(value) }
 }
 return out
}

func (s *Service) RunReadOnly(cmd string)(string,error) {
 ok:=false;for _,v:=range allowed{if cmd==v{ok=true;break}};if strings.HasPrefix(cmd,"show vlan "){id,err:=strconv.Atoi(strings.TrimPrefix(cmd,"show vlan "));if err==nil&&id>=1&&id<=4094{ok=true}}
 if !ok{return "",fmt.Errorf("polecenie niedozwolone")}
 if s.cfg.Host==""||s.cfg.SSHUser==""||s.cfg.SSHPassword==""{return "",fmt.Errorf("brak SWITCH_HOST lub danych SSH w .env")}
 c,e:=ssh.Dial("tcp",net.JoinHostPort(s.cfg.Host,s.cfg.SSHPort),&ssh.ClientConfig{User:s.cfg.SSHUser,Auth:[]ssh.AuthMethod{ssh.Password(s.cfg.SSHPassword)},HostKeyCallback:ssh.InsecureIgnoreHostKey(),HostKeyAlgorithms:[]string{ssh.KeyAlgoRSA},Timeout:s.cfg.Timeout})
 if e!=nil{return "",e};defer c.Close()
 ss,e:=c.NewSession();if e!=nil{return "",e};defer ss.Close()
 b,e:=ss.Output(cmd);return string(b),e
}

func validProbeTarget(target string) bool {
 target=strings.TrimSpace(target)
 if ip:=net.ParseIP(target);ip!=nil{return ip.To4()!=nil}
 if len(target)<1||len(target)>253{return false}
 for _,label:=range strings.Split(target,"."){if len(label)<1||len(label)>63||label[0]=='-'||label[len(label)-1]=='-'{return false};for _,r:=range label{if !(r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'||r=='-'){return false}}}
 return true
}
func (s *Service) ProbeNetwork(kind,target string)(string,error){
 target=strings.TrimSpace(target)
 if !validProbeTarget(target){return "",fmt.Errorf("podaj poprawny adres IPv4 lub nazwę DNS")}
 var cmd string
 switch kind{case "ping":cmd="ping "+target;case "traceroute":cmd="traceroute "+target;default:return "",fmt.Errorf("nieznany test sieciowy")}
 return s.RunCLICommand(cmd)
}
func (s *Service) RunCLICommand(cmd string)(string,error){
 if s.cfg.Host==""||s.cfg.SSHUser==""||s.cfg.SSHPassword==""{return "",fmt.Errorf("brak SWITCH_HOST lub danych SSH w .env")}
 client,e:=ssh.Dial("tcp",net.JoinHostPort(s.cfg.Host,s.cfg.SSHPort),&ssh.ClientConfig{User:s.cfg.SSHUser,Auth:[]ssh.AuthMethod{ssh.Password(s.cfg.SSHPassword)},HostKeyCallback:ssh.InsecureIgnoreHostKey(),HostKeyAlgorithms:[]string{ssh.KeyAlgoRSA},Timeout:s.cfg.Timeout})
 if e!=nil{return "",e};defer client.Close()
 session,e:=client.NewSession();if e!=nil{return "",e};defer session.Close()
 output,e:=session.Output(cmd);return string(output),e
}
func (s *Service) ConfigureStaticRoute(prefix,gateway string,distance int,remove bool)(string,error){
 _,network,e:=net.ParseCIDR(strings.TrimSpace(prefix));if e!=nil{return "",fmt.Errorf("podaj poprawną sieć CIDR, np. 192.168.20.0/24")}
 ip:=net.ParseIP(gateway);if ip==nil||ip.To4()==nil{return "",fmt.Errorf("brama musi być poprawnym adresem IPv4")}
 if distance<1||distance>255{return "",fmt.Errorf("dystans administracyjny musi być w zakresie 1–255")}
 command:="ip route "+network.String()+" "+ip.String()
 if distance!=1{command+=" "+strconv.Itoa(distance)}
 if remove{command="no "+command}
 return s.runConfig([]string{"configure terminal",command,"end"})
}

func validVLANID(v int)bool{return v>=1&&v<=4094}
func parseVLANList(raw string)([]int,error) {
 raw=strings.TrimSpace(raw);if raw==""{return nil,fmt.Errorf("podaj co najmniej jeden VLAN")}
 seen:=map[int]bool{};out:=[]int{}
 for _,part:=range strings.Split(raw,",") {
  part=strings.TrimSpace(part)
  if strings.Contains(part,"-") {
   a,b,ok:=strings.Cut(part,"-");if !ok{return nil,fmt.Errorf("nieprawidłowy zakres VLAN")}
   lo,e1:=strconv.Atoi(strings.TrimSpace(a));hi,e2:=strconv.Atoi(strings.TrimSpace(b))
   if e1!=nil||e2!=nil||lo<1||hi>4094||lo>hi||hi-lo>256{return nil,fmt.Errorf("nieprawidłowy zakres VLAN: %s",part)}
   for n:=lo;n<=hi;n++{if !seen[n]{seen[n]=true;out=append(out,n)}}
  } else {
   n,e:=strconv.Atoi(part);if e!=nil||!validVLANID(n){return nil,fmt.Errorf("nieprawidłowy VLAN: %s",part)}
   if !seen[n]{seen[n]=true;out=append(out,n)}
  }
 }
 if len(out)==0{return nil,fmt.Errorf("pusta lista VLAN")}
 return out,nil
}
func validIPv4(s string) bool { ip:=net.ParseIP(strings.TrimSpace(s)); return ip!=nil && ip.To4()!=nil }
func (s *Service) ConfigureDHCPPool(name,network,mask,low,high,router,dns string,enable bool)(string,error) {
 name=strings.TrimSpace(name)
 if len(name)<1||len(name)>32||!regexp.MustCompile("^[A-Za-z0-9_-]+$").MatchString(name){return "",fmt.Errorf("nazwa puli: 1–32 znaków A-Z, a-z, 0-9, _ lub -")}
 for label,value:=range map[string]string{"sieć":network,"maska":mask,"początek zakresu":low,"koniec zakresu":high,"brama":router,"DNS":dns}{if !validIPv4(value){return "",fmt.Errorf("nieprawidłowy adres IPv4 (%s)",label)}}
 n:=net.ParseIP(network).To4();m:=net.IPMask(net.ParseIP(mask).To4());ones,bits:=m.Size()
 if bits!=32||ones<1||ones>30{return "",fmt.Errorf("maska musi być poprawną maską IPv4 od /1 do /30")}
 ipnet:=&net.IPNet{IP:n.Mask(m),Mask:m}
 lo,hi,gw:=net.ParseIP(low).To4(),net.ParseIP(high).To4(),net.ParseIP(router).To4()
 if !ipnet.Contains(lo)||!ipnet.Contains(hi)||!ipnet.Contains(gw){return "",fmt.Errorf("zakres i brama muszą należeć do podanej podsieci")};if !n.Equal(ipnet.IP){return "",fmt.Errorf("adres sieci musi być adresem sieci, np. 192.168.10.0") }
 if bytesCompare(lo,hi)>0{return "",fmt.Errorf("początek zakresu jest większy niż koniec")}
 if lo.Equal(ipnet.IP)||hi.Equal(ipnet.IP)||lo.Equal(net.IP{byte(n[0]|^m[0]),byte(n[1]|^m[1]),byte(n[2]|^m[2]),byte(n[3]|^m[3])}){return "",fmt.Errorf("zakres nie może zawierać adresu sieci ani broadcast")}
 if bytesCompare(gw,lo)>=0&&bytesCompare(gw,hi)<=0{return "",fmt.Errorf("wyłącz adres bramy z zakresu DHCP")}
 cmds:=[]string{"configure terminal","ip dhcp pool "+name,"network "+network+" "+mask,"range "+low+" "+high,"default-router "+router,"dns-server "+dns,"exit"}
 if enable {cmds=append(cmds,"service dhcp-server")}
 cmds=append(cmds,"end")
 return s.runConfig(cmds)
}
func bytesCompare(a,b net.IP) int { for i:=0;i<4;i++ {if a[i]<b[i]{return -1};if a[i]>b[i]{return 1}};return 0 }

func (s *Service) ConfigureVLAN(id int,name string,remove bool)(string,error) {
 if id<2||id>4094{return "",fmt.Errorf("VLAN ID musi być z zakresu 2–4094; VLAN 1 jest domyślny")}
 if remove{return s.runConfig([]string{"configure terminal","vlan database",fmt.Sprintf("no vlan %d",id),"exit","end"})}
 name=strings.TrimSpace(name)
 if name!="" {ok,_:=regexp.MatchString("^[A-Za-z0-9_-]+$",name);if len(name)>16||!ok{return "",fmt.Errorf("nazwa VLAN: 1–16 znaków A-Z, a-z, 0-9, _ lub -")}}
 cmd:=fmt.Sprintf("vlan %d state enable",id);if name!=""{cmd=fmt.Sprintf("vlan %d name %s state enable",id,name)}
 return s.runConfig([]string{"configure terminal","vlan database",cmd,"exit","end"})
}
func (s *Service) ConfigurePortSettings(name,mode string,access int,allowed string,native int,speed,duplex,description string)(string,error) {
 if !validPort(name){return "",fmt.Errorf("nieprawidłowy port")}
 if mode!="access"&&mode!="trunk"{return "",fmt.Errorf("tryb portu musi być access lub trunk")}
 if speed!="auto"&&speed!="10"&&speed!="100"&&speed!="1000"{return "",fmt.Errorf("prędkość musi wynosić auto, 10, 100 lub 1000 Mb/s")}
 if duplex!="auto"&&duplex!="full"&&duplex!="half"{return "",fmt.Errorf("duplex musi być auto, full lub half")}
 description=strings.TrimSpace(description)
 ok,_:=regexp.MatchString("^[A-Za-z0-9 _./:-]*$",description)
 if len(description)>64||!ok{return "",fmt.Errorf("opis portu: maks. 64 znaki; dozwolone litery łacińskie, cyfry, spacja i _ . / : -")}
 cmds:=[]string{"configure terminal","interface "+name}
 if description==""{cmds=append(cmds,"no description")}else{cmds=append(cmds,"description "+description)}
 cmds=append(cmds,"speed "+speed,"duplex "+duplex,"switchport")
 if mode=="access" {
  if access<1||access>4094{return "",fmt.Errorf("access VLAN musi być z zakresu 1–4094")}
  cmds=append(cmds,"switchport mode access",fmt.Sprintf("switchport access vlan %d",access))
 } else {
  vlans,e:=parseVLANList(allowed);if e!=nil{return "",e}
  if native<1||native>4094{return "",fmt.Errorf("native VLAN musi być z zakresu 1–4094")}
  found:=false;for _,v:=range vlans{if v==native{found=true}}
  if !found{return "",fmt.Errorf("native VLAN musi znajdować się na liście dozwolonych VLAN-ów")}
  canonical:=make([]string,0,len(vlans));for _,v:=range vlans{canonical=append(canonical,strconv.Itoa(v))}
  cmds=append(cmds,"switchport mode trunk","switchport trunk allowed vlan none","switchport trunk allowed vlan add "+strings.Join(canonical,","),fmt.Sprintf("switchport trunk native vlan %d",native))
 }
 cmds=append(cmds,"end")
 return s.runConfig(cmds)
}
func parsePortNumbers(raw string)([]int,error) {
 seen:=map[int]bool{};out:=[]int{}
 for _,p:=range strings.Split(raw,",") {p=strings.TrimSpace(p);n,e:=strconv.Atoi(p);if e!=nil||n<1||n>50{return nil,fmt.Errorf("porty podaj jako listę numerów 1–50, np. 5,6")};if !seen[n]{seen[n]=true;out=append(out,n)}}
 if len(out)<2{return nil,fmt.Errorf("agregacja wymaga co najmniej dwóch portów")}
 sort.Ints(out);return out,nil
}
func (s *Service) ConfigureLACP(group int,rawPorts,mode string,remove bool)(string,error) {
 if group<1||group>32{return "",fmt.Errorf("numer grupy LACP musi być z zakresu 1–32")}
 ports,e:=parsePortNumbers(rawPorts);if e!=nil{return "",e}
 if mode!="active"&&mode!="passive"{return "",fmt.Errorf("tryb LACP musi być active lub passive")}
 names:=make([]string,0,len(ports));for _,n:=range ports{names=append(names,fmt.Sprintf("port1.0.%d",n))}
 cmd:="channel-group "+strconv.Itoa(group)+" mode "+mode;if remove{cmd="no channel-group"}
 return s.runConfig([]string{"configure terminal","interface "+strings.Join(names,","),cmd,"end"})
}
func (s *Service) ConfigureSTP(mode string,priority int)(string,error) {
 if mode!="rstp"&&mode!="mstp"&&mode!="stp"{return "",fmt.Errorf("tryb STP musi być stp, rstp lub mstp")}
 if priority<0||priority>61440||priority%4096!=0{return "",fmt.Errorf("priorytet musi być wielokrotnością 4096 w zakresie 0–61440")}
 return s.runConfig([]string{"configure terminal","spanning-tree mode "+mode,fmt.Sprintf("spanning-tree priority %d",priority),"end"})
}
func (s *Service) SetPortEdgePort(name string,enabled bool)(string,error) {
 if !validPort(name){return "",fmt.Errorf("nieprawidłowy port")}
 cmd:="no spanning-tree edgeport";if enabled{cmd="spanning-tree edgeport"}
 return s.runConfig([]string{"configure terminal","interface "+name,cmd,"end"})
}
