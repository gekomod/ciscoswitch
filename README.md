# CiscoSwitch Control Center — Allied Telesis AT-GS948MX

Panel WWW do monitorowania i konfiguracji przełącznika Allied Telesis AT-GS948MX z AlliedWare Plus. Repozytorium zachowuje nazwę ciscoswitch, ale adapter CLI jest przeznaczony dla Allied Telesis, nie Cisco IOS.

## Moduły
- Dashboard z odczytem portów SNMP v2c / IF-MIB (porty 1–52, w tym dwa SFP+ 10G i dwa porty combo).
- Włączanie i wyłączanie portu.
- VLAN: podgląd listy, tworzenie i usuwanie VLAN 2–4094.
- Porty: access/trunk, VLAN access, lista VLAN dozwolonych na trunk, native VLAN.
- Porty: opis, speed auto/10/100/1000, duplex auto/full/half.
- LACP: tworzenie i usuwanie channel-group, tryb active/passive, podgląd etherchannel.
- STP: ustawienie trybu STP/RSTP/MSTP, priorytet bridge, edgeport na porcie.
- DHCP client dla interfejsu zarządzającego VLAN 1.
- Zapis running-config do startup-config.
- Eksport running-config i startup-config do plików tekstowych z poziomu panelu (wymaga logowania).
- Porównanie running-config i startup-config z listą dodanych oraz usuniętych linii.
- Diagnostyka CLI z allowlistą poleceń show.

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
LISTEN_ADDR=127.0.0.1:8090
PANEL_USER=admin
PANEL_PASSWORD=USE_A_RANDOM_PASSWORD_AT_LEAST_16_CHARS
AUDIT_LOG_PATH=data/audit.jsonl
```

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
- Panel wymaga logowania HTTP Basic: `PANEL_USER` i `PANEL_PASSWORD` (minimum 16 znaków). Bez tych danych nie uruchomi się.
- Wszystkie połączenia SSH weryfikują SHA256 fingerprint z `SWITCH_SSH_HOST_KEY`. Wartość musi być pobrana z zaufanego źródła; przykładowy placeholder nie zadziała.
- Panel domyślnie nasłuchuje na localhost. Przy udostępnieniu w sieci używaj wyłącznie za HTTPS (np. reverse proxy) i ogranicz dostęp do sieci zarządzającej. HTTP Basic nie szyfruje poświadczeń.
- Nie commituj pliku .env, haseł ani community SNMP.

## Dodatkowe moduły
- Diagnostyka systemu: wersja, CPU, pamięć, środowisko, boot.
- Routing statyczny i testy ping/traceroute.
- SNMP: diagnostyka agenta, odbiorca trapów v2c oraz tworzenie/usuwanie użytkownika SNMPv3 authPriv (SHA + DES).
- SSH: podgląd i zarządzanie lokalnymi użytkownikami; panel blokuje usunięcie konta wskazanego w `SWITCH_SSH_USER`, aby nie odciąć własnego dostępu.
- DHCP server/pule — składnia wymaga potwierdzenia na konkretnej wersji firmware przed użyciem produkcyjnym.

## Ważne
Eksport konfiguracji może zawierać sekrety i dane administracyjne — przechowuj pliki jak hasła. Porównanie konfiguracji nie zapisuje kopii i nie zmienia urządzenia. Przywracanie konfiguracji z pliku nie jest jeszcze udostępnione; nie wykonuj go przez ręczne wklejanie niezweryfikowanych komend.

SNMPv3 używa grupy priv i użytkownika SHA + DES. Składnia bazuje na oficjalnym AlliedWare Plus Best Practice Guide; potwierdź ją na firmware urządzenia przed użyciem produkcyjnym. Nie wykonano jeszcze testów integracyjnych na fizycznym AT-GS948MX. CI sprawdza składnię JS, testy Go i kompilację, ale nie potwierdza obsługi każdej komendy przez urządzenie.


### Lokalne kopie konfiguracji

Panel pozwala ręcznie tworzyć kopie `running-config` na serwerze, przeglądać historię i pobierać pliki. Dodatkowo przed każdą zmianą konfiguracji wykonywana jest automatyczna kopia; jeśli odczyt lub zapis kopii się nie powiedzie, zmiana zostaje zablokowana. Domyślny katalog to `data/config-backups` (można zmienić przez `CONFIG_BACKUP_DIR`). Domyślnie przechowywanych jest 100 najnowszych kopii; limit można ustawić przez `CONFIG_BACKUP_RETENTION` (1–10000). Starsze kopie są usuwane automatycznie. Katalog ma uprawnienia `0700`, pliki `0600`; katalog `data/` jest wykluczony z Git. Kopie zawierają poufne dane urządzenia — zabezpiecz system plików i wykonuj własne kopie katalogu. Metadane historii zawierają datę, rozmiar i SHA-256. Nowe kopie mają osobny plik sumy kontrolnej; panel weryfikuje go przy wyświetlaniu historii i pobieraniu. Kopia ze zmienioną zawartością jest oznaczana jako niezweryfikowana, a jej pobranie blokowane. Starsze kopie bez pliku sumy pozostają dostępne jako niezweryfikowane. Funkcja nie przywraca konfiguracji. Dziennik operacji API zapisuje czas, zalogowanego użytkownika panelu, metodę, ścieżkę endpointu, kod HTTP i czas obsługi. Domyślnie trafia do `data/audit.jsonl` (zmiana przez `AUDIT_LOG_PATH`), z uprawnieniami katalogu `0700` i pliku `0600`. Dziennik nie zapisuje treści żądań, haseł, community SNMP, wyników CLI ani konfiguracji.
