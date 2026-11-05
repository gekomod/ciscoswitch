package device

import (
 "fmt"
 "net"
 "sort"
 "strconv"
 "strings"
 "time"
 "github.com/gosnmp/gosnmp"
 "golang.org/x/crypto/ssh"
)
type Config struct{Host,SSHPort,SSHUser,SSHPassword string;SNMPCommunity,SNMPPort string;Timeout time.Duration}
type Service struct{cfg Config}
type Port struct{Index int `json:"index"`;Name string `json:"name"`;Admin string `json:"admin"`;Oper string `json:"oper"`;Speed string `json:"speed"`;MTU uint32 `json:"mtu"`}
type Status struct{Host string `json:"host"`;Reachable bool `json:"reachable"`;Ports []Port `json:"ports"`;Warning string `json:"warning,omitempty"`}
func New(c Config)*Service{return &Service{cfg:c}}
func(s *Service)Status()Status{
 o:=Status{Host:s.cfg.Host,Ports:[]Port{}}
 if s.cfg.Host==""||s.cfg.SNMPCommunity==""{o.Warning="Brak SWITCH_HOST lub SWITCH_SNMP_COMMUNITY. Sprawdź .env i uruchom panel ponownie.";return o}
 g:=*gosnmp.Default;g.Target=s.cfg.Host;g.Port=uint16(port(s.cfg.SNMPPort,161));g.Community=s.cfg.SNMPCommunity;g.Version=gosnmp.Version2c;g.Timeout=s.cfg.Timeout;g.Retries=1
 if e:=g.Connect();e!=nil{o.Warning="SNMP connect: "+e.Error();return o};defer g.Conn.Close()
 d,e:=walkText(&g,".1.3.6.1.2.1.2.2.1.2");if e!=nil{o.Warning="IF-MIB ifDescr: "+e.Error();return o};if len(d)==0{o.Warning="SNMP działa, ale IF-MIB ifDescr jest pusty.";return o};o.Reachable=true
 mtu,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.4");speed,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.5");adm,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.7");op,_:=walkNum(&g,".1.3.6.1.2.1.2.2.1.8")
 ix:=make([]int,0,len(d));for i:=range d{ix=append(ix,i)};sort.Ints(ix)
 for _,i:=range ix{p:=Port{Index:i,Name:d[i],Admin:state(adm[i]),Oper:state(op[i]),MTU:mtu[i]};if speed[i]>0{p.Speed=fmt.Sprintf("%d Mb/s",speed[i]/1000000)};o.Ports=append(o.Ports,p)}
 return o
}
func walkText(g *gosnmp.GoSNMP,oid string)(map[int]string,error){m:=map[int]string{};e:=g.Walk(oid,func(p gosnmp.SnmpPDU)error{a:=strings.Split(p.Name,".");i,x:=strconv.Atoi(a[len(a)-1]);if x!=nil{return nil};switch v:=p.Value.(type){case []byte:m[i]=strings.TrimSpace(string(v));case string:m[i]=strings.TrimSpace(v);default:m[i]=fmt.Sprint(v)};return nil});return m,e}
func walkNum(g *gosnmp.GoSNMP,oid string)(map[int]uint32,error){m:=map[int]uint32{};e:=g.Walk(oid,func(p gosnmp.SnmpPDU)error{a:=strings.Split(p.Name,".");i,x:=strconv.Atoi(a[len(a)-1]);if x!=nil{return nil};switch v:=p.Value.(type){case uint:m[i]=uint32(v);case uint32:m[i]=v;case int:if v>0{m[i]=uint32(v)};case int64:if v>0{m[i]=uint32(v)};case uint64:m[i]=uint32(v)};return nil});return m,e}
func state(v uint32)string{switch v{case 1:return "up";case 2:return "down";case 3:return "testing";default:return "unknown"}}
func validPort(v string)bool{if !strings.HasPrefix(v,"port1.0."){return false};n,e:=strconv.Atoi(strings.TrimPrefix(v,"port1.0."));return e==nil&&n>=1&&n<=50}
func(s *Service)ConfigurePort(name,action string)(string,error){if !validPort(name){return "",fmt.Errorf("nieprawidłowy port")};cmd:="shutdown";if action=="enable"{cmd="no shutdown"}else if action!="disable"{return "",fmt.Errorf("nieobsługiwana operacja")};return s.runConfig([]string{"configure terminal","interface "+name,cmd,"end"})}
func(s *Service)SetManagementDHCP(on bool)(string,error){cmd:="no ip address dhcp";if on{cmd="ip address dhcp"};return s.runConfig([]string{"configure terminal","interface vlan1",cmd,"end"})}
func(s *Service)SaveConfig()(string,error){return s.runConfig([]string{"copy running-config startup-config"})}
func(s *Service)runConfig(cmds []string)(string,error){
 if s.cfg.Host==""||s.cfg.SSHUser==""||s.cfg.SSHPassword==""{return "",fmt.Errorf("brak konfiguracji SSH w .env")}
 c,e:=ssh.Dial("tcp",net.JoinHostPort(s.cfg.Host,s.cfg.SSHPort),&ssh.ClientConfig{User:s.cfg.SSHUser,Auth:[]ssh.AuthMethod{ssh.Password(s.cfg.SSHPassword)},HostKeyCallback:ssh.InsecureIgnoreHostKey(),HostKeyAlgorithms:[]string{ssh.KeyAlgoRSA},Timeout:s.cfg.Timeout});if e!=nil{return "",e};defer c.Close()
 ss,e:=c.NewSession();if e!=nil{return "",e};defer ss.Close();in,e:=ss.StdinPipe();if e!=nil{return "",e};var out strings.Builder;ss.Stdout=&out;ss.Stderr=&out
 if e=ss.Shell();e!=nil{return "",e};for _,x:=range cmds{if _,e=fmt.Fprintln(in,x);e!=nil{return out.String(),e}};_ = in.Close()
 done:=make(chan error,1);go func(){done<-ss.Wait()}();select{case e=<-done:if e!=nil{return out.String(),fmt.Errorf("CLI: %w; %s",e,out.String())};case <-time.After(8*time.Second):_ = ss.Close();return out.String(),fmt.Errorf("timeout CLI")};return out.String(),nil
}
var allowed=[]string{"show system","show version","show interfaces status","show interfaces","show vlan","show ip interface brief","show lldp neighbors","show spanning-tree","show boot","show http"}
func(s *Service)RunReadOnly(cmd string)(string,error){ok:=false;for _,v:=range allowed{if cmd==v{ok=true;break}};if !ok{return "",fmt.Errorf("polecenie niedozwolone")};if s.cfg.Host==""||s.cfg.SSHUser==""{return "",fmt.Errorf("brak SWITCH_HOST lub SWITCH_SSH_USER")};c,e:=ssh.Dial("tcp",net.JoinHostPort(s.cfg.Host,s.cfg.SSHPort),&ssh.ClientConfig{User:s.cfg.SSHUser,Auth:[]ssh.AuthMethod{ssh.Password(s.cfg.SSHPassword)},HostKeyCallback:ssh.InsecureIgnoreHostKey(),HostKeyAlgorithms:[]string{ssh.KeyAlgoRSA},Timeout:s.cfg.Timeout});if e!=nil{return "",e};defer c.Close();ss,e:=c.NewSession();if e!=nil{return "",e};defer ss.Close();b,e:=ss.Output(cmd);return string(b),e
}
func port(v string,f int)int{n,e:=strconv.Atoi(v);if e!=nil||n<1||n>65535{return f};return n}
