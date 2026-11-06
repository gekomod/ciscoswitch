package device

import (
 "reflect"
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
 for _,v:=range []string{"port1.0.1","port1.0.50"} { if !validPort(v){t.Errorf("%s should be valid",v)} }
 for _,v:=range []string{"port1.0.0","port1.0.51","port1.1.1","port1.0.1;shutdown"} { if validPort(v){t.Errorf("%s should be invalid",v)} }
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
