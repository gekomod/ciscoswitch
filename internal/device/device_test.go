package device

import (
 "reflect"
 "strings"
 "testing"
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
