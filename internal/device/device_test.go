package device

import (
 "reflect"
 "strings"
 "testing"

 "github.com/gosnmp/gosnmp"
)

func TestParseVLANList(t *testing.T) {
 got,err:=parseVLANList("10, 20-22,10")
 if err!=nil { t.Fatal(err) }
 want:=[]int{10,20,21,22}
 if !reflect.DeepEqual(got,want) { t.Fatalf("got %v, want %v",got,want) }
}
func TestParseVLANListRejectsInvalidInput(t *testing.T) {
 for _,v:=range []string{"","0","4095","10;shutdown","20-10","1, 5000"} {
  if _,err:=parseVLANList(v);err==nil { t.Errorf("expected error for %q",v) }
 }
}
func TestValidPort(t *testing.T) {
 for _,v:=range []string{"port1.0.1","port1.0.48","port1.0.49","port1.0.50","port1.0.51","port1.0.52"} { if !validPort(v){t.Errorf("%s should be valid",v)} }
 for _,v:=range []string{"port1.0.0","port1.0.53","port1.1.1","port1.0.1;shutdown"} { if validPort(v){t.Errorf("%s should be invalid",v)} }
}
func TestConfigurePortSettingsRejectsUnsafeDescription(t *testing.T) {
 s:=New(Config{})
 _,err:=s.ConfigurePortSettings("port1.0.2","access",10,"",1,"auto","auto","AP; shutdown")
 if err==nil { t.Fatal("expected unsafe description to be rejected") }
}
func TestConfigureVLANRejectsReservedAndInvalidIDs(t *testing.T) {
 s:=New(Config{})
 for _,id:=range []int{0,1,4095} { if _,err:=s.ConfigureVLAN(id,"",false);err==nil {t.Errorf("expected VLAN %d to be rejected",id)} }
}
func TestParsePortNumbers(t *testing.T) {
 got,err:=parsePortNumbers("6,5,6")
 if err!=nil {t.Fatal(err)}
 if !reflect.DeepEqual(got,[]int{5,6}) {t.Fatalf("unexpected port list: %v",got)}
 for _,v:=range []string{"5","1,53","1;shutdown,2",""} {if _,err:=parsePortNumbers(v);err==nil{t.Errorf("expected invalid port list %q",v)}}
}
func TestConfigureSTPValidation(t *testing.T) {
 s:=New(Config{})
 if _,err:=s.ConfigureSTP("rstp",8192);err==nil||!strings.Contains(err.Error(),"brak konfiguracji SSH"){t.Fatalf("valid values should pass validation and reach SSH config check; got %v",err)}
 if _,err:=s.ConfigureSTP("rapid",8192);err==nil{t.Fatal("invalid STP mode should be rejected")}
 if _,err:=s.ConfigureSTP("rstp",9000);err==nil{t.Fatal("invalid bridge priority should be rejected")}
}

func TestParsePortNumbersIncludesSFPPlusPorts(t *testing.T) {
 got,err:=parsePortNumbers("1,48,51,52")
 if err!=nil { t.Fatal(err) }
 want:=[]int{1,48,51,52}
 if !reflect.DeepEqual(got,want) { t.Fatalf("got %v, want %v",got,want) }
}

func TestConfigureSSHUserCannotDeletePanelAccount(t *testing.T) {
 s:=New(Config{SSHUser:"manager"})
 if _,err:=s.ConfigureSSHUser("manager","",0,true);err==nil {
  t.Fatal("expected active panel SSH account deletion to be blocked")
 }
}


func TestBuildPortSettingsCommandsPreviewDoesNotNeedSSH(t *testing.T) {
 cmds,err:=BuildPortSettingsCommands("port1.0.7","trunk",1,"10,20-21",10,"auto","auto","AP biuro")
 if err!=nil{t.Fatal(err)}
 joined:=strings.Join(cmds,"\\n")
 for _,want:=range []string{"interface port1.0.7","switchport mode trunk","switchport trunk allowed vlan add 10,20,21","switchport trunk native vlan 10"}{
  if !strings.Contains(joined,want){t.Fatalf("preview missing %q: %v",want,cmds)}
 }
 if _,err:=BuildPortSettingsCommands("port1.0.7","trunk",1,"10",20,"auto","auto","");err==nil{t.Fatal("native VLAN outside allowed list must fail preview")}
}

func TestBuildVLANCommandsPreview(t *testing.T) {
 cmds,err:=BuildVLANCommands(30,"IOT",false);if err!=nil{t.Fatal(err)}
 joined:=strings.Join(cmds,"\\n")
 if !strings.Contains(joined,"vlan 30 name IOT state enable"){t.Fatalf("unexpected create preview: %v",cmds)}
 del,err:=BuildVLANCommands(30,"",true);if err!=nil{t.Fatal(err)}
 if !strings.Contains(strings.Join(del,"\\n"),"no vlan 30"){t.Fatalf("unexpected delete preview: %v",del)}
}

func TestBuildSNMPv3UserCommands(t *testing.T) {
 cmds, err := BuildSNMPv3UserCommands("monitor01", "monitoring", "AuthPass123!", "PrivPass456!", false)
 if err != nil { t.Fatal(err) }
 joined := strings.Join(cmds, "\n")
 for _, want := range []string{"snmp-server view panel-readonly 1.3.6.1.2.1 included", "snmp-server group monitoring priv read panel-readonly", "snmp-server user monitor01 monitoring auth sha AuthPass123! priv des PrivPass456!"} {
  if !strings.Contains(joined, want) { t.Fatalf("preview missing %q: %v", want, cmds) }
 }
 del, err := BuildSNMPv3UserCommands("monitor01", "", "", "", true)
 if err != nil { t.Fatal(err) }
 if !strings.Contains(strings.Join(del, "\n"), "no snmp-server user monitor01") { t.Fatalf("unexpected delete preview: %v", del) }
}
func TestBuildSNMPv3UserCommandsRejectsUnsafeInput(t *testing.T) {
 cases := [][4]string{
  {"bad user", "monitoring", "AuthPass123!", "PrivPass456!"},
  {"monitor01", "bad;group", "AuthPass123!", "PrivPass456!"},
  {"monitor01", "monitoring", "short", "PrivPass456!"},
  {"monitor01", "monitoring", "SamePass123!", "SamePass123!"},
  {"monitor01", "monitoring", "Auth Pass123!", "PrivPass456!"},
 }
 for _, c := range cases {
  if _, err := BuildSNMPv3UserCommands(c[0], c[1], c[2], c[3], false); err == nil { t.Errorf("expected invalid input to be rejected: %#v", c) }
 }
}

func TestNewSNMPClientV2cCompatibility(t *testing.T) {
 g,err:=newSNMPClient(Config{Host:"127.0.0.1",SNMPCommunity:"readonly",SNMPPort:"161"})
 if err!=nil {t.Fatal(err)}
 if g.Version!=gosnmp.Version2c || g.Community!="readonly" {t.Fatalf("unexpected v2c config: version=%v community=%q",g.Version,g.Community)}
}
func TestNewSNMPClientV3AuthPriv(t *testing.T) {
 g,err:=newSNMPClient(Config{Host:"127.0.0.1",SNMPVersion:"3",SNMPV3User:"monitor01",SNMPV3AuthPassword:"AuthPass123!",SNMPV3PrivacyPassword:"PrivPass456!"})
 if err!=nil {t.Fatal(err)}
 if g.Version!=gosnmp.Version3 || g.SecurityModel!=gosnmp.UserSecurityModel || g.MsgFlags!=gosnmp.AuthPriv {t.Fatalf("unexpected SNMPv3 security settings: version=%v model=%v flags=%v",g.Version,g.SecurityModel,g.MsgFlags)}
 p,ok:=g.SecurityParameters.(*gosnmp.UsmSecurityParameters);if !ok {t.Fatalf("unexpected security parameters type %T",g.SecurityParameters)}
 if p.UserName!="monitor01" || p.AuthenticationProtocol!=gosnmp.SHA || p.PrivacyProtocol!=gosnmp.DES {t.Fatalf("unexpected SNMPv3 protocols or username: %+v",p)}
}
func TestNewSNMPClientRejectsIncompleteV3(t *testing.T) {
 cases:=[]Config{
  {Host:"127.0.0.1",SNMPVersion:"3"},
  {Host:"127.0.0.1",SNMPVersion:"3",SNMPV3User:"monitor01",SNMPV3AuthPassword:"short",SNMPV3PrivacyPassword:"PrivPass456!"},
  {Host:"127.0.0.1",SNMPVersion:"4",SNMPV3User:"monitor01",SNMPV3AuthPassword:"AuthPass123!",SNMPV3PrivacyPassword:"PrivPass456!"},
 }
 for _,c:=range cases {if _,err:=newSNMPClient(c);err==nil{t.Errorf("expected invalid SNMP configuration to fail: %+v",c)}}
}

func TestBuildSNMPv3TrapHostCommands(t *testing.T) {
 cmds,err:=BuildSNMPv3TrapHostCommands("192.0.2.20","monitor01",false)
 if err!=nil{t.Fatal(err)}
 if got:=strings.Join(cmds,"\n");!strings.Contains(got,"snmp-server host 192.0.2.20 version 3 priv monitor01"){t.Fatalf("unexpected SNMPv3 trap command: %v",cmds)}
 del,err:=BuildSNMPv3TrapHostCommands("192.0.2.20","monitor01",true)
 if err!=nil{t.Fatal(err)}
 if got:=strings.Join(del,"\n");!strings.Contains(got,"no snmp-server host 192.0.2.20 version 3 priv monitor01"){t.Fatalf("unexpected SNMPv3 trap delete: %v",del)}
}
func TestBuildSNMPv3TrapHostRejectsUnsafeInput(t *testing.T) {
 for _,v:=range [][2]string{{"not-an-ip","monitor01"},{"192.0.2.20","bad user"},{"192.0.2.20","monitor01;shutdown"}} {
  if _,err:=BuildSNMPv3TrapHostCommands(v[0],v[1],false);err==nil{t.Errorf("expected invalid trap target to be rejected: %#v",v)}
 }
}

func TestBuildBulkPortSettingsCommands(t *testing.T) {
 cmds,err:=BuildBulkPortSettingsCommands([]string{"port1.0.5","port1.0.6","port1.0.5"},"access",10,"",1,"auto","auto","BIURO")
 if err!=nil{t.Fatal(err)}
 joined:=strings.Join(cmds,"\n")
 if strings.Count(joined,"interface port1.0.5")!=1||strings.Count(joined,"interface port1.0.6")!=1{t.Fatalf("ports not deduplicated: %v",cmds)}
 if cmds[0]!="configure terminal"||cmds[len(cmds)-1]!="end"{t.Fatalf("missing config context: %v",cmds)}
}
func TestBuildBulkPortSettingsCommandsRejectsInvalidBatch(t *testing.T) {
 for _,ports:=range [][]string{{},{"port1.0.5","bad port"}} {
  if _,err:=BuildBulkPortSettingsCommands(ports,"access",10,"",1,"auto","auto","");err==nil{t.Errorf("expected invalid batch to fail: %v",ports)}
 }
}

func TestBuildBulkPortActionCommands(t *testing.T) {
 cmds,err:=BuildBulkPortActionCommands([]string{"port1.0.5","port1.0.6","port1.0.5"},"enable")
 if err!=nil{t.Fatal(err)}
 joined:=strings.Join(cmds,"\n")
 if strings.Count(joined,"interface port1.0.5")!=1||strings.Count(joined,"interface port1.0.6")!=1{t.Fatalf("ports not deduplicated: %v",cmds)}
 if !strings.Contains(joined,"no shutdown")||cmds[0]!="configure terminal"||cmds[len(cmds)-1]!="end"{t.Fatalf("unexpected commands: %v",cmds)}
 if _,err:=BuildBulkPortActionCommands([]string{"port1.0.1"},"disable");err!=nil{t.Fatal(err)}
 for _,tc:=range []struct{ports []string;action string}{{nil,"enable"},{[]string{"bad"},"enable"},{[]string{"port1.0.1"},"restart"}} {
  if _,err:=BuildBulkPortActionCommands(tc.ports,tc.action);err==nil{t.Errorf("expected rejection: %#v",tc)}
 }
}

func TestBuildBulkPortEdgePortCommands(t *testing.T) {
 cmds,err:=BuildBulkPortEdgePortCommands([]string{"port1.0.2","port1.0.3","port1.0.2"},true)
 if err!=nil{t.Fatal(err)}
 joined:=strings.Join(cmds,"\n")
 if strings.Count(joined,"interface port1.0.2")!=1||!strings.Contains(joined,"spanning-tree edgeport"){t.Fatalf("unexpected commands: %v",cmds)}
 if _,err:=BuildBulkPortEdgePortCommands([]string{"bad"},true);err==nil{t.Fatal("expected invalid port rejection")}
}

func TestBuildVLANPortMembershipCommandsPreservesExistingTrunk(t *testing.T) {
 config := `interface port1.0.5
 switchport
 switchport mode trunk
 switchport trunk allowed vlan add 10,20
 switchport trunk native vlan 1
!
interface port1.0.6
 switchport
 switchport mode trunk
 switchport trunk native vlan 10
!`
 cmds,err:=BuildVLANPortMembershipCommands(config,[]string{"port1.0.5"},30,"tagged")
 if err!=nil{t.Fatal(err)}
 joined:=strings.Join(cmds,"\n")
 if !strings.Contains(joined,"switchport trunk allowed vlan add 30"){t.Fatalf("missing tagged add: %v",cmds)}
 if strings.Contains(joined,"allowed vlan none"){t.Fatalf("must preserve existing trunk VLANs: %v",cmds)}
 if _,err:=BuildVLANPortMembershipCommands(config,[]string{"port1.0.6"},10,"tagged");err==nil{t.Fatal("native VLAN cannot also be added as tagged")}
}
func TestBuildVLANPortMembershipCommandsAccessOnlyForUntagged(t *testing.T) {
 config := `interface port1.0.5
 switchport
 switchport mode access
 switchport access vlan 20
!
interface port1.0.6
 switchport
 switchport mode trunk
 switchport trunk native vlan 1
!`
 cmds,err:=BuildVLANPortMembershipCommands(config,[]string{"port1.0.5"},30,"untagged")
 if err!=nil{t.Fatal(err)}
 if !strings.Contains(strings.Join(cmds,"\n"),"switchport access vlan 30"){t.Fatalf("missing access VLAN assignment: %v",cmds)}
 if _,err:=BuildVLANPortMembershipCommands(config,[]string{"port1.0.6"},30,"untagged");err==nil{t.Fatal("must not silently convert trunk to access")}
}
func TestBuildVLANPortMembershipCommandsRejectsInvalidAndRoutedPorts(t *testing.T) {
 config := `interface port1.0.5
 no switchport
!`
 for _,tc:=range []struct{ports []string;vlan int;membership string}{{[]string{"port1.0.5"},30,"untagged"},{[]string{"bad"},30,"tagged"},{[]string{"port1.0.5"},4095,"tagged"},{[]string{"port1.0.5"},30,"native"}} {
  if _,err:=BuildVLANPortMembershipCommands(config,tc.ports,tc.vlan,tc.membership);err==nil{t.Errorf("expected rejection: %#v",tc)}
 }
}
