# CiscoSwitch Control Center — Allied Telesis AT-GS948MX

Panel WWW do monitorowania i konfiguracji przełącznika Allied Telesis AT-GS948MX z AlliedWare Plus. Repozytorium zachowuje nazwę ciscoswitch, ale adapter CLI jest przeznaczony dla Allied Telesis, nie Cisco IOS.

## Moduły
- Wyszukiwanie sekcji w bocznej nawigacji; skrót `Ctrl+K` lub `/`, filtrowanie bez przeładowania strony.
- Dashboard z odczytem portów IF-MIB przez SNMPv2c lub SNMPv3 authPriv (SHA/DES), wybieranym w `.env` (porty 1–52, w tym dwa SFP+ 10G i dwa porty combo).
- Włączanie i wyłączanie portu.
- VLAN: podgląd listy, tworzenie i usuwanie VLAN 2–4094.
- Porty: access/trunk, VLAN access, lista VLAN dozwolonych na trunk, native VLAN.
- Porty: opis, speed auto/10/100/1000, duplex auto/full/half.
- LACP: tworzenie i usuwanie channel-group, tryb active/passive oraz diagnostyka tylko do odczytu `show etherchannel detail` (agregatory, partner LAG, liczniki łączy i synchronizacja portów). Parser jest zależny od formatu CLI; surowa odpowiedź pozostaje dostępna.
- STP: ustawienie trybu STP/RSTP/MSTP, priorytet bridge, edgeport na porcie.
- DHCP client dla interfejsu zarządzającego VLAN 1.
- Zapis running-config do startup-config.
- Eksport running-config i startup-config do plików tekstowych z poziomu panelu (wymaga logowania).
- Porównanie running-config i startup-config z listą dodanych oraz usuniętych linii.
- Diagnostyka CLI z allowlistą poleceń show.
- Monitoring RX/TX per port, wykres ostatnich 60 próbek oraz historia błędów i zmian linku.
- Topologia sieci: wizualizacja portów i urządzeń z FDB/MAC, ARP, LLDP oraz stanu SNMP; lokalne rozpoznawanie ograniczonej listy producentów po prefiksie MAC (OUI). Nieznane prefiksy pozostają nierozpoznane, a mapa nie jest gwarantowanym schematem fizycznego okablowania.
- Topologia: ostrożna klasyfikacja urządzeń wyłącznie na podstawie jawnych słów w opisie LLDP (switch/router/AP/kamera/drukarka/telefon/komputer); OUI nie służy do ustalania typu urządzenia. Port z sąsiadem LLDP opisanym jako switch/router i aktywnym linkiem jest oznaczany jako prawdopodobny uplink. Odczyt `show spanning-tree` pokazuje rozpoznane stany forwarding oraz alternate/discarding/blocking; nie oznacza automatycznie wykrycia pętli. Klasyfikacja i parser STP są heurystyczne i zależą od formatu CLI.
- Mapa VLAN i urządzeń: odczyt `show vlan ID` zestawia członkostwo tagged/untagged z ostatnio wykrytymi MAC/IP/LLDP na portach. Dodatkowo pokazuje wiele bloków obserwacji LLDP i ostrożne wskazówki o możliwej redundancji uplinków na podstawie LLDP/STP; nie jest to wykrywanie pętli ani pełny audyt ścieżek.
- Historia ruchu per port jest zapisywana lokalnie w `data/monitor-history.jsonl` i odtwarzana po ponownym otwarciu panelu; retencja jest ograniczona do 60 próbek na port w odpowiedzi API, a plik ma limit 2 MiB i uprawnienia 0600.

## Instalacja — Debian 13

```bash
git clone https://github.com/gekomod/ciscoswitch.git
cd ciscoswitch
cp .env.example .env
nano .env
go mod tidy
go run .
```

Panel domyślnie słucha tylko na `127.0.0.1:8090`.

## Konfiguracja .env

```dotenv
SWITCH_HOST=192.168.31.2
SWITCH_SSH_PORT=22
SWITCH_SSH_USER=manager
SWITCH_SSH_PASSWORD=CHANGE_ME
SWITCH_SSH_HOST_KEY=SHA256:REPLACE_WITH_VERIFIED_FINGERPRINT
SWITCH_SNMP_COMMUNITY=CHANGE_ME_READONLY
SWITCH_SNMP_PORT=161
SWITCH_SNMP_VERSION=2c
# For SNMPv3 authPriv, set SWITCH_SNMP_VERSION=3 and configure the following secrets:
SWITCH_SNMP_V3_USER=
SWITCH_SNMP_V3_AUTH_PASSWORD=
SWITCH_SNMP_V3_PRIV_PASSWORD=
LISTEN_ADDR=127.0.0.1:8090
PANEL_USER=admin
PANEL_PASSWORD=USE_A_RANDOM_PASSWORD_AT_LEAST_16_CHARS
AUDIT_LOG_PATH=data/audit.jsonl
```

## Błąd SSH: niezgodny klucz hosta

Komunikat `oczekiwano SHA256:REPLACE_WITH_VERIFIED_FINGERPRINT` oznacza, że w lokalnym pliku `.env` pozostała wartość przykładowa. Nie jest to błąd hasła SSH ani SNMP.

1. Zweryfikuj odcisk klucza SSH switcha niezależnym, zaufanym kanałem (np. konsola lokalna urządzenia i dokumentacja/konfiguracja jego serwera SSH). Sam komunikat błędu nie dowodzi autentyczności klucza.
2. Po potwierdzeniu odcisku wpisz go w **lokalnym** `.env` jako `SWITCH_SSH_HOST_KEY=SHA256:... ` (bez spacji na końcu).
3. Uruchom ponownie proces aplikacji, ponieważ `.env` jest odczytywany podczas startu. Dla `go run .` zatrzymaj proces i uruchom ponownie; dla usługi systemd uruchom ponownie rzeczywistą nazwę usługi.

Możesz podejrzeć odcisk prezentowany przez urządzenie z sieci zarządzającej (to **nie** zastępuje niezależnej weryfikacji):

```bash
ssh-keyscan -T 5 -p 22 -t rsa,ecdsa,ed25519 192.168.31.2 2>/dev/null | ssh-keygen -lf - -E sha256
```

Jeśli pojawi się kilka odcisków, upewnij się, który algorytm klucza hosta jest negocjowany przez aplikację. Nie ustawiaj `ssh.InsecureIgnoreHostKey`, nie wyłączaj kontroli odcisku i nie zapisuj lokalnego `.env` do GitHub.

## Bezpieczeństwo i ograniczenia
- Wyłączanie portów jest domyślnie zablokowane; świadome odblokowanie wymaga `ALLOW_PORT_SHUTDOWN=true`. Nie ustawiaj tej opcji bez lokalnego planu odzyskania dostępu.
- Zmiany klienta DHCP na interfejsie zarządzającym VLAN 1 są domyślnie zablokowane; odblokowanie wymaga `ALLOW_MANAGEMENT_DHCP_CHANGE=true` i zapewnienia alternatywnego dostępu.
- LACP nie może objąć interfejsu znajdującego się na liście `PROTECTED_PORTS`.
- `PROTECTED_PORTS` przyjmuje listę interfejsów oddzielonych przecinkami (np. `port1.0.49,port1.0.52`). Panel blokuje dla nich wyłączenie i zmianę ustawień. Wpisz wyłącznie porty wcześniej zidentyfikowane jako uplinki/zarządzające; przykładowych numerów nie należy kopiować w ciemno.

- DHCP oznacza klienta DHCP interfejsu VLAN 1; nie jest to serwer DHCP dla komputerów.
- Zmiana VLAN na uplinku lub adresu DHCP może odciąć dostęp do przełącznika. Zmiany wymagają potwierdzenia w UI.
- Sprawdź konfigurację na porcie testowym przed zmianą portów produkcyjnych.
- Prędkości 10/100/1000 i duplex auto/full/half są oparte na instrukcji AlliedWare Plus dla miedzianych portów RJ-45. Zakres możliwości konkretnego portu zależy od sprzętu.
- Składnia VLAN/access/trunk jest zgodna z opisami AlliedWare Plus. Dokumentacja referencyjna użyta przy implementacji: Allied Telesis How To Configure Basic Switching Functionality oraz AlliedWare Plus 5.4.6 Command Reference.
- Panel wymaga logowania HTTP Basic: `PANEL_USER` i `PANEL_PASSWORD` (minimum 8 znaków). Bez tych danych nie uruchomi się.
- Wszystkie połączenia SSH weryfikują SHA256 fingerprint z `SWITCH_SSH_HOST_KEY`. Wartość musi być pobrana z zaufanego źródła; przykładowy placeholder nie zadziała.
- Panel domyślnie nasłuchuje na localhost. Przy udostępnieniu w sieci używaj wyłącznie za HTTPS (np. reverse proxy) i ogranicz dostęp do sieci zarządzającej. HTTP Basic nie szyfruje poświadczeń.
- Nie commituj pliku .env, haseł ani community SNMP.

## Dodatkowe moduły
- Diagnostyka systemu: wersja, CPU, pamięć, środowisko, boot.
- Routing statyczny i testy ping/traceroute.
- SNMP: diagnostyka agenta, odbiorcy Trap v2c/v3 oraz tworzenie/usuwanie użytkownika SNMPv3 authPriv (SHA + DES).
- SSH: podgląd i zarządzanie lokalnymi użytkownikami; panel blokuje usunięcie konta wskazanego w `SWITCH_SSH_USER`, aby nie odciąć własnego dostępu.
- DHCP server/pule — składnia wymaga potwierdzenia na konkretnej wersji firmware przed użyciem produkcyjnym.

## Ważne
Eksport konfiguracji może zawierać sekrety i dane administracyjne — przechowuj pliki jak hasła. Porównanie konfiguracji nie zapisuje kopii i nie zmienia urządzenia. Przywracanie konfiguracji z pliku nie jest jeszcze udostępnione; nie wykonuj go przez ręczne wklejanie niezweryfikowanych komend.

SNMPv3 Trap: odbiorca jest konfigurowany komendą `snmp-server host IP version 3 priv USER` i może być dodany po utworzeniu użytkownika. SNMPv3 monitoring: ustaw `SWITCH_SNMP_VERSION=3`, `SWITCH_SNMP_V3_USER`, `SWITCH_SNMP_V3_AUTH_PASSWORD` i `SWITCH_SNMP_V3_PRIV_PASSWORD` w `.env`, a następnie zrestartuj panel. Używany jest poziom authPriv (SHA/DES); community v2c nie jest wtedy wymagana. Sekrety pozostają poza repozytorium i logami. Nie wykonano jeszcze testów integracyjnych na fizycznym AT-GS948MX. CI sprawdza składnię JS, testy Go i kompilację, ale nie potwierdza obsługi każdej komendy przez urządzenie.


### Lokalne kopie konfiguracji

Panel pozwala ręcznie tworzyć kopie `running-config` na serwerze, przeglądać historię i pobierać pliki. Dodatkowo przed każdą zmianą konfiguracji wykonywana jest automatyczna kopia; jeśli odczyt lub zapis kopii się nie powiedzie, zmiana zostaje zablokowana. Domyślny katalog to `data/config-backups` (można zmienić przez `CONFIG_BACKUP_DIR`). Domyślnie przechowywanych jest 100 najnowszych kopii; limit można ustawić przez `CONFIG_BACKUP_RETENTION` (1–10000). Starsze kopie są usuwane automatycznie. Katalog ma uprawnienia `0700`, pliki `0600`; katalog `data/` jest wykluczony z Git. Kopie zawierają poufne dane urządzenia — zabezpiecz system plików i wykonuj własne kopie katalogu. Metadane historii zawierają datę, rozmiar i SHA-256. Nowe kopie mają osobny plik sumy kontrolnej; panel weryfikuje go przy wyświetlaniu historii i pobieraniu. Kopia ze zmienioną zawartością jest oznaczana jako niezweryfikowana, a jej pobranie blokowane. Starsze kopie bez pliku sumy pozostają dostępne jako niezweryfikowane. Funkcja nie przywraca konfiguracji. Dziennik operacji API zapisuje czas, zalogowanego użytkownika panelu, metodę, ścieżkę endpointu, kod HTTP i czas obsługi. Domyślnie trafia do `data/audit.jsonl` (zmiana przez `AUDIT_LOG_PATH`), z uprawnieniami katalogu `0700` i pliku `0600`. Dziennik nie zapisuje treści żądań, haseł, community SNMP, wyników CLI ani konfiguracji.
