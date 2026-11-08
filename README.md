# CiscoSwitch Control Center — Allied Telesis AT-GS948MX

Panel WWW do monitorowania i konfiguracji przełącznika Allied Telesis AT-GS948MX z AlliedWare Plus. Repozytorium zachowuje nazwę ciscoswitch, ale adapter CLI jest przeznaczony dla Allied Telesis, nie Cisco IOS.

## Moduły
- Wyszukiwanie sekcji w bocznej nawigacji; skrót `Ctrl+K` lub `/`, filtrowanie bez przeładowania strony.
- Dashboard z odczytem portów IF-MIB przez SNMPv2c lub SNMPv3 authPriv (SHA/DES), wybieranym w `.env` (porty 1–52, w tym dwa SFP+ 10G i dwa porty combo).
- Włączanie i wyłączanie portu.
- VLAN: podgląd listy, tworzenie i usuwanie VLAN 2–4094.
- Porty: access/trunk, VLAN access, lista VLAN dozwolonych na trunk, native VLAN.
- Porty: opis, speed auto/10/100/1000, duplex auto/full/half.
- Biblioteka profili portów Access/Trunk: własne profile z nazwą, opisem, opisem interfejsu, VLAN, native VLAN, allowed VLAN, speed i duplex. Profile są przechowywane lokalnie w `data/port-profiles.json`; można je zapisywać, aktualizować, usuwać i wczytywać do operacji masowej. Zastosowanie nadal wymaga zbiorczego podglądu, potwierdzenia, kopii konfiguracji i odczytu zwrotnego.
- LACP: tworzenie i usuwanie channel-group, tryb active/passive oraz diagnostyka tylko do odczytu z próbą `show etherchannel detail`, `show etherchannel summary` i `show etherchannel` (agregatory, partner LAG, liczniki łączy i synchronizacja portów, jeśli format jest rozpoznany). Parser jest zależny od formatu CLI; panel pokazuje rozpoznane agregatory i stan portów, a nieprzetworzone odpowiedzi pozostają poza standardowym widokiem użytkownika.
- STP: ustawienie trybu STP/RSTP/MSTP, priorytet bridge, edgeport na porcie.
- DHCP client dla interfejsu zarządzającego VLAN 1.
- Zapis running-config do startup-config.
- Eksport running-config i startup-config do plików tekstowych z poziomu panelu (wymaga logowania).
- Porównanie running-config i startup-config oraz zweryfikowanej kopii z bieżącą konfiguracją. Panel pokazuje wyłącznie liczbę dodanych/usuniętych linii, bez wyświetlania ich treści ani sekretów.
- Diagnostyka CLI z allowlistą poleceń show.
- Monitoring RX/TX per port, wykres ostatnich 60 próbek oraz historia błędów i zmian linku.
- Audyt ról portów: ostrożne rozpoznawanie Access Point, CCTV, VoIP, serwerów, stacji roboczych i uplinków wyłącznie na podstawie jawnych słów w opisie interfejsu. Panel pokazuje sugerowany profil do rozważenia; nie wyprowadza VLAN-ów ani nie zmienia konfiguracji. Brak jednoznacznego opisu oznacza rolę nieustaloną.
- Topologia sieci: wizualizacja portów i urządzeń z FDB/MAC, ARP, LLDP oraz stanu SNMP; lokalne rozpoznawanie ograniczonej listy producentów po prefiksie MAC (OUI). Nieznane prefiksy pozostają nierozpoznane, a mapa nie jest gwarantowanym schematem fizycznego okablowania.
- Topologia: ostrożna klasyfikacja urządzeń wyłącznie na podstawie jawnych słów w opisie LLDP (switch/router/AP/kamera/drukarka/telefon/komputer); OUI nie służy do ustalania typu urządzenia. Port z sąsiadem LLDP opisanym jako switch/router i aktywnym linkiem jest oznaczany jako prawdopodobny uplink. Odczyt `show spanning-tree` pokazuje rozpoznane stany forwarding oraz alternate/discarding/blocking; nie oznacza automatycznie wykrycia pętli. Klasyfikacja i parser STP są heurystyczne i zależą od formatu CLI.
- Analiza wpływu redundancji: dla zdegradowanego LACP, niespójności członków i pojedynczego jawnie opisanego uplinku panel pokazuje rozpoznane porty skonfigurowane, członków widocznych operacyjnie, potencjalnie dotknięte VLAN-y oraz sąsiadów LLDP. VLAN-y są prezentowane wyłącznie, gdy istnieje ich odczyt; brak danych jest jawnie oznaczany. To potencjalny zakres wpływu, a nie potwierdzenie przerwania ruchu ani kompletna analiza ścieżki end-to-end.
- Mapa VLAN: przy każdym porcie dostępny jest skrót do okna konfiguracji portu; sama mapa pozostaje odczytem członkostwa tagged/untagged. Zaznaczone porty można przekazać do istniejącego profilu masowego bez ręcznego przepisywania numerów; zastosowanie nadal wymaga podglądu i potwierdzenia.
- Mapa VLAN: można dodać wybrany VLAN jako tagged na portach, które już pracują w trunk, albo ustawić go jako untagged na portach access. Backend odczytuje running-config i blokuje automatyczną konwersję access↔trunk oraz konflikt z native VLAN; lista istniejących VLAN na trunk pozostaje zachowana. Każda zmiana wymaga podglądu, potwierdzenia i kopii running-config. Porty z `PROTECTED_PORTS` są blokowane.
- Mapa VLAN i urządzeń: odczyt `show vlan ID` zestawia członkostwo tagged/untagged z ostatnio wykrytymi MAC/IP/LLDP na portach. Dodatkowo pokazuje wiele bloków obserwacji LLDP i ostrożne wskazówki o możliwej redundancji uplinków na podstawie LLDP/STP; nie jest to wykrywanie pętli ani pełny audyt ścieżek.

- Centrum diagnostyki awarii: serwer zbiera automatycznie read-only próbki znormalizowanego stanu LACP, jawnie opisanych uplinków, sąsiadów LLDP i dostępności odczytów. Pierwsza próbka jest zbierana przy starcie, kolejne według `DIAGNOSTICS_INTERVAL` (domyślnie `60s`, minimum `10s`, `0` wyłącza sampler). Historia jest zapisywana atomowo w lokalnym pliku JSON z uprawnieniami katalogu 0700 i pliku 0600; domyślna retencja wynosi 12 000 próbek (około 8,3 dnia przy interwale 60 sekund). API `POST /api/diagnostics/collect` nadal pozwala ręcznie zebrać próbkę, a `GET /api/diagnostics/history?limit=100&windowMinutes=60` zwraca historię, alarmy i zmiany z całego zachowanego okna. Trzy lub więcej zmian tego samego stanu LACP w wybranym oknie generują alarm powtarzających się zmian (flapping). Dostępne okna: 5–1440 minut. LACP Ready=0 jest alarmem krytycznym, rozbieżność członków — ostrzeżeniem, a pojedynczy jawnie opisany uplink — informacją o ryzyku topologii, nie awarii. Brak danych nie jest traktowany jako powrót do normy. Krótkie zaniki krótsze niż interwał próbkowania mogą pozostać niewykryte. Ścieżkę pliku można zmienić przez `DIAGNOSTICS_HISTORY_PATH`.
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
PORT_PROFILES_PATH=data/port-profiles.json
DIAGNOSTICS_HISTORY_PATH=data/diagnostics-history.json
DIAGNOSTICS_INTERVAL=60s
MONITOR_INTERVAL=30s
MONITOR_HISTORY_PATH=data/monitor-history.jsonl
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

## Role panelu

Panel obsługuje trwały rejestr wielu kont. Przy pierwszym uruchomieniu, jeśli plik `PANEL_USERS_PATH` (domyślnie `data/users.json`) nie istnieje, konto początkowe jest tworzone z `PANEL_USER`, `PANEL_PASSWORD` i `PANEL_ROLE`. Przy kolejnych uruchomieniach rejestr jest źródłem prawdy; zmiany tych zmiennych nie nadpisują istniejących kont.

Role:
- `viewer` — podgląd danych; bez zmian konfiguracji, eksportu konfiguracji, pobierania backupów i administracji kontami.
- `operator` — odczyt oraz operacje konfiguracyjne; bez administracji kontami panelu.
- `admin` — pełny dostęp, w tym zarządzanie użytkownikami.

Administrator zarządza kontami w sekcji „Użytkownicy i role”: może tworzyć konta, zmieniać role, blokować/odblokowywać dostęp i resetować hasła. Nowe hasła muszą mieć co najmniej 12 znaków. Hasła są przechowywane jako bcrypt; API nigdy nie zwraca hashy. Nie można zablokować ani zdegradować ostatniego aktywnego administratora. Zmiana roli, blokada konta lub reset hasła unieważnia jego dotychczasowe sesje. Konta panelu są niezależne od kont SSH na przełączniku.

Autoryzacja jest egzekwowana po stronie backendu. Sesja otrzymuje rolę przy logowaniu; endpoint `/api/session` zwraca nazwę zalogowanego konta i rolę. Rejestr JSON jest zapisywany atomowo, z uprawnieniami katalogu `0700` i pliku `0600`. Wykonaj kopię `data/users.json` razem z innymi lokalnymi danymi panelu; nie publikuj tego pliku w repozytorium. Uszkodzony lub pusty rejestr blokuje start zamiast automatycznie tworzyć nowe konto i nadpisywać dane.

## Bezpieczeństwo i ograniczenia
- Wyłączanie portów jest domyślnie zablokowane; świadome odblokowanie wymaga `ALLOW_PORT_SHUTDOWN=true`. Nie ustawiaj tej opcji bez lokalnego planu odzyskania dostępu.
- Zmiany klienta DHCP na interfejsie zarządzającym VLAN 1 są domyślnie zablokowane; odblokowanie wymaga `ALLOW_MANAGEMENT_DHCP_CHANGE=true` i zapewnienia alternatywnego dostępu.
- LACP nie może objąć interfejsu znajdującego się na liście `PROTECTED_PORTS`.
- `PROTECTED_PORTS` przyjmuje listę interfejsów oddzielonych przecinkami (np. `port1.0.49,port1.0.52`). Panel blokuje dla nich wyłączenie, zmianę profilu portu, ustawień EdgePort i dodanie do LACP. Wpisz wyłącznie porty wcześniej zidentyfikowane jako uplinki/zarządzające; przykładowych numerów nie należy kopiować w ciemno.

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
- Diagnostyka systemu: wersja firmware, CPU, pamięć, temperatura, wentylatory, zasilacze i uptime w formie czytelnych wskaźników; nieparsowane odpowiedzi CLI nie są prezentowane użytkownikowi.
- Routing statyczny i testy ping/traceroute.
- SNMP: diagnostyka agenta, odbiorcy Trap v2c/v3 oraz tworzenie/usuwanie użytkownika SNMPv3 authPriv (SHA + DES).
- SSH: podgląd i zarządzanie lokalnymi użytkownikami; panel blokuje usunięcie konta wskazanego w `SWITCH_SSH_USER`, aby nie odciąć własnego dostępu.
- DHCP server/pule — składnia wymaga potwierdzenia na konkretnej wersji firmware przed użyciem produkcyjnym.

## Ważne
Eksport konfiguracji może zawierać sekrety i dane administracyjne — przechowuj pliki jak hasła. Porównanie konfiguracji nie zapisuje kopii i nie zmienia urządzenia. Przywracanie konfiguracji z pliku nie jest jeszcze udostępnione; nie wykonuj go przez ręczne wklejanie niezweryfikowanych komend.

SNMPv3 Trap: odbiorca jest konfigurowany komendą `snmp-server host IP version 3 priv USER` i może być dodany po utworzeniu użytkownika. SNMPv3 monitoring: ustaw `SWITCH_SNMP_VERSION=3`, `SWITCH_SNMP_V3_USER`, `SWITCH_SNMP_V3_AUTH_PASSWORD` i `SWITCH_SNMP_V3_PRIV_PASSWORD` w `.env`, a następnie zrestartuj panel. Używany jest poziom authPriv (SHA/DES); community v2c nie jest wtedy wymagana. Sekrety pozostają poza repozytorium i logami. Nie wykonano jeszcze testów integracyjnych na fizycznym AT-GS948MX. CI sprawdza składnię JS, testy Go i kompilację, ale nie potwierdza obsługi każdej komendy przez urządzenie.


### Lokalne kopie konfiguracji

Panel pozwala ręcznie tworzyć kopie `running-config` na serwerze, przeglądać historię i pobierać pliki. Dodatkowo przed każdą zmianą konfiguracji wykonywana jest automatyczna kopia; jeśli odczyt lub zapis kopii się nie powiedzie, zmiana zostaje zablokowana. Domyślny katalog to `data/config-backups` (można zmienić przez `CONFIG_BACKUP_DIR`). Domyślnie przechowywanych jest 100 najnowszych kopii; limit można ustawić przez `CONFIG_BACKUP_RETENTION` (1–10000). Starsze kopie są usuwane automatycznie. Katalog ma uprawnienia `0700`, pliki `0600`; katalog `data/` jest wykluczony z Git. Kopie zawierają poufne dane urządzenia — zabezpiecz system plików i wykonuj własne kopie katalogu. Metadane historii zawierają datę, rozmiar i SHA-256. Nowe kopie mają osobny plik sumy kontrolnej; panel weryfikuje go przy wyświetlaniu historii i pobieraniu. Kopia ze zmienioną zawartością jest oznaczana jako niezweryfikowana, a jej pobranie blokowane. Starsze kopie bez pliku sumy pozostają dostępne jako niezweryfikowane. Funkcja nie przywraca konfiguracji. Zweryfikowane kopie można porównywać z aktualnym `running-config`; kopie bez pliku SHA-256 lub z niezgodną sumą są blokowane. Wynik porównania zawiera tylko liczbę różnic i liczbę linii — bez treści konfiguracji. Szczegółowe różnice nadal mogą być sprawdzone ręcznie w bezpiecznym narzędziu poza panelem, ale eksportowane pliki traktuj jak sekrety. Dziennik operacji API zapisuje czas, zalogowanego użytkownika panelu, metodę, ścieżkę endpointu, kod HTTP i czas obsługi. Domyślnie trafia do `data/audit.jsonl` (zmiana przez `AUDIT_LOG_PATH`), z uprawnieniami katalogu `0700` i pliku `0600`. Dziennik nie zapisuje treści żądań, haseł, community SNMP, wyników CLI ani konfiguracji.


### Inspektor portu / switchport
W szczegółach wybranego portu przycisk „Odczytaj VLAN / MAC / LLDP” pobiera dane z `show interface switchport`, tablicy VLAN, FDB/MAC i LLDP. Panel prezentuje zwięzłe podsumowanie trybu access/trunk, native/PVID, rozpoznanych VLAN-ów, adresów MAC i sąsiadów LLDP. Surowe odpowiedzi CLI nie są wyświetlane w tym widoku. Dopasowanie odbywa się po pełnej nazwie interfejsu; brakujące dane są jawnie oznaczane. Odczyt jest tylko do odczytu i nie zmienia konfiguracji. Status linku i liczniki nadal pochodzą z SNMP/IF-MIB.


### Centrum alarmów
Panel agreguje zapisane zdarzenia monitoringu w jednym widoku, pokazuje liczniki według poziomu i umożliwia filtrowanie historii. Zdarzenia dostępności SNMP, zmiany linku portów i przyrosty liczników błędów zbiera niezależny sampler po stronie serwera — również wtedy, gdy żadna przeglądarka nie jest otwarta.

## Monitoring na żywo

Dashboard odczytuje stan przełącznika przez `/api/status` co 15 sekund i parametry systemowe co 60 sekund, gdy karta panelu jest widoczna. Dodatkowo proces Go zbiera dostępność SNMP, zmiany stanu linków i przyrosty liczników błędów niezależnie od przeglądarki. Interwał ustawia `MONITOR_INTERVAL` (domyślnie `30s`, minimum `10s`, `0` wyłącza kolektor); ścieżkę historii ustawia `MONITOR_HISTORY_PATH` (domyślnie `data/monitor-history.jsonl`). Historia jest lokalna, z ograniczeniem rozmiaru pliku i uprawnieniami 0600. Próbki RX/TX nadal powstają podczas odczytów dashboardu, ponieważ wyliczają tempo transferu z różnicy liczników między kolejnymi pomiarami.

Endpoint `GET /api/monitor/stream` udostępnia uwierzytelniony strumień Server-Sent Events (SSE). Zdarzenia zapisane przez aktywne sesje panelu są rozsyłane do pozostałych otwartych sesji bez przeładowywania strony. Wolni odbiorcy nie blokują monitoringu; po rozłączeniu przeglądarka automatycznie nawiązuje połączenie ponownie. Strumień używa wyłącznie metadanych zdarzeń, bez surowego wyjścia CLI, konfiguracji ani sekretów.

**Zakres i ograniczenia:** kolektor serwerowy wykrywa zmiany pomiędzy próbkami; krótkie zaniki krótsze niż interwał mogą pozostać niewykryte. Utrata całej łączności SNMP jest raportowana jako brak osiągalności, a nie jako awaria konkretnego portu. Wykresy tempa RX/TX są nadal uzupełniane przez aktywny dashboard. Serwer wysyła heartbeat SSE co 20 sekund, aby utrzymać połączenie przez proxy.

### Audyt bezpieczeństwa konfiguracji

Moduł „Audyt bezpieczeństwa konfiguracji” wykonuje wyłącznie odczyty `show running-config` oraz `show interface switchport`. Wykrywa jawne wpisy wskazujące na domyślne community SNMP (`public` / `private`), włączony HTTP/Telnet, wyłączenie STP, niespójność native VLAN z listą VLAN trunk, EdgePort na portach trunk/LACP oraz wybrane rozbieżności między deklaracjami VLAN i przypisaniami interfejsów. VLAN bez jawnego przypisania jest oznaczany jako kandydat do weryfikacji — nie jako pewny VLAN nieużywany. Audyt nie zmienia konfiguracji i nie proponuje automatycznego zastosowania poprawek. Jeśli odczyt switchport jest niedostępny, pozostałe wyniki pozostają dostępne, a analiza trunków jest oznaczana jako niekompletna. Wyniki zależą od formatu konfiguracji zwracanego przez firmware; brak wykrycia nie jest certyfikatem bezpieczeństwa. Nie wykonano jeszcze walidacji na fizycznym AT-GS948MX.

### Mapa topologii i ryzyka połączeń

Sekcja w audycie bezpieczeństwa pobiera lokalny running-config, show interface switchport, show lldp neighbors i show etherchannel detail. Pokazuje rozpoznane porty, jawne opisy i role (rola uplink jest przypisywana wyłącznie po słowach w opisie), VLAN-y, członków agregacji LACP i sąsiadów LLDP. Nieznane formaty pozostają niedostępne zamiast być zgadywane. Brak wpisu LLDP nie jest interpretowany jako brak fizycznego łącza. Widok jest wyłącznie diagnostyczny i nie zmienia konfiguracji. Analiza ryzyka wskazuje pojedynczy jawnie opisany uplink jako kandydata na pojedynczy punkt awarii, agregacje z jednym skonfigurowanym członkiem, niespójność członków skonfigurowanych i rozpoznanych operacyjnie oraz LACP zgłaszające Ready=0. Porty wpisane do `PROTECTED_PORTS` są oznaczone jako chronione. Brak LLDP na opisanym uplinku jest wyłącznie informacją o braku potwierdzenia sąsiedztwa, nie dowodem braku łącza. Wnioski nie zastępują weryfikacji topologii po stronie sąsiednich urządzeń ani planu zmian. Podgląd konfiguracji pojedynczego portu, podgląd zbiorczy oraz podgląd naprawy częściowej pobierają dodatkowo kontekst topologii: jawny opis roli, członkostwo LACP i rozpoznane sąsiedztwo LLDP. Ostrzeżenia o uplinku, członku agregacji lub sąsiedzie są pokazywane przed potwierdzeniem zmiany; nie blokują samodzielnie operacji, ale wymagają oceny operatora. Po potwierdzeniu frontend przekazuje jawne potwierdzenie ryzyka. Backend sprawdza kontekst topologii przed utworzeniem kopii, aby nie tworzyć zbędnego backupu dla odrzuconej operacji, a następnie ponownie odczytuje go po wykonaniu kopii bezpośrednio przed zapisem. Operacja jest odrzucana, jeśli wykryto ryzyko, lecz nie zostało potwierdzone. Dla podglądu zbiorczego wykonywany jest jeden wspólny odczyt switchport oraz dwa odczyty kontekstu topologii. Porty z `PROTECTED_PORTS` nadal są blokowane przez backend. Podgląd zbiorczy raportuje trzy odczyty CLI: jeden wspólny switchport oraz dwa odczyty kontekstu topologii (running-config i LLDP).

- Potwierdzanie alarmów: operator może potwierdzić aktywny alarm i dodać komentarz. Potwierdzenia są zapisywane lokalnie w pliku `DIAGNOSTICS_HISTORY_PATH` z sufiksem `.acks.json`, z katalogiem 0700 i plikiem 0600. Potwierdzenie jest przypisane do konkretnej wersji alarmu (kod, dowód i czas); jeśli stan alarmu się zmienił, backend odrzuca nieaktualne potwierdzenie. Historia zachowuje operatora, czas i komentarz. Potwierdzenie nie kasuje alarmu ani nie oznacza usunięcia przyczyny.

- API potwierdzania aktywnych alarmów: `POST /api/diagnostics/acknowledge` przyjmuje identyfikator alarmu i komentarz operatora (maks. 500 znaków). Potwierdzenie jest powiązane z aktualnym kodem, dowodem i znacznikiem czasu; nieaktualne alarmy są odrzucane. `GET /api/diagnostics/history` dołącza stan potwierdzenia, operatora, czas i komentarz. Alarmy flappingu są wyliczane dynamicznie z okna i nie mają ręcznego potwierdzenia — znikają po ustaniu warunku.


### Dostępność i ciągłość pomiarów

Centrum diagnostyki udostępnia podsumowanie 24-godzinne i 7-dniowe. Wskaźnik urządzenia opisuje wyłącznie osiągalność uwierzytelnionego CLI (udany odczyt `show running-config`), a nie dostępność ruchu użytkowników, SNMP ani portów. Dla agregacji LACP wskaźnik opiera się na rozpoznanym stanie operacyjnym agregacji. Czas jest liczony tylko między kolejnymi próbkami, których odstęp nie przekracza dwukrotności skonfigurowanego interwału. Przedziały z brakującymi lub zbyt odległymi próbkami są raportowane jako „brak danych” i nie są uznawane za awarię ani dostępność. Wynik jest orientacyjną metryką obserwacji, nie gwarantowanym SLA. Endpoint: `GET /api/diagnostics/availability?window=24h` lub `window=7d`.


### Raporty dostępności i eksport

Panel dostępności pokazuje wykres godzinowy dla 24 godzin oraz dzienny dla 7 dni. Słupki bez wystarczających danych są oznaczane jako brak pomiaru, a nie jako 0% dostępności. Eksport CSV jest dostępny pod `GET /api/diagnostics/availability.csv?window=24h` lub `window=7d`. Plik zawiera podsumowanie urządzenia i każdej agregacji LACP, pokrycie pomiarami, czas nieznany oraz osobne wiersze dla wykrytych przerw. CSV jest kodowany UTF-8 z BOM i używa separatora średnikowego, aby poprawnie otwierać się w polskim Excelu.


### Centrum zdarzeń sieciowych

Endpoint `GET /api/events?window=24h&type=all&limit=200` udostępnia wspólną oś czasu alarmów, zmian LACP, sąsiadów LLDP i wyników kontroli zarządzających. Okna: 24h, 7d i 30d; kategorie: all, alarm, lacp, lldp i availability; limit 1–500. Zdarzenia są wyliczane z historii próbek i sortowane od najnowszych, bez dopisywania duplikatów przy odświeżaniu. Zmiany LLDP obejmują wykrycie, zniknięcie i zmianę sąsiada. Oś czasu alarmów pokazuje potwierdzenie, operatora i komentarz. Nieudana kontrola CLI jest opisana jako brak odczytu zarządzającego, a nie jako potwierdzone wyłączenie switcha lub przerwanie ruchu.


### Korelacja zdarzeń

Endpoint `GET /api/events` w widoku kategorii `all` zwraca również pole `correlations`. Grupowanie obejmuje zdarzenia różnych kategorii występujące w jednym pięciominutowym oknie, jeśli przynajmniej jedno ma poziom ostrzeżenia lub krytyczny. Panel pokazuje kategorie, liczbę zdarzeń i zakres czasu. Jest to wskazówka diagnostyczna oparta na zbieżności czasowej — nie dowód wspólnej przyczyny ani automatyczna diagnoza awarii.


## Konta i role panelu

Panel ma osobne konta lokalne, niezależne od użytkowników SSH skonfigurowanych na przełączniku.

Przy pierwszym uruchomieniu ustaw na serwerze panelu:
- `PANEL_ADMIN_USER` — login początkowego administratora.
- `PANEL_ADMIN_PASSWORD` — hasło początkowego administratora (minimum 12 znaków).
- `PANEL_USERS_PATH` — opcjonalna ścieżka pliku kont; domyślnie `data/panel-users.json`.

Przykład konfiguracji środowiska (nie wpisuj haseł do repozytorium):
```bash
export PANEL_ADMIN_USER=admin
export PANEL_ADMIN_PASSWORD='zmien-to-na-dlugie-losowe-haslo'
export PANEL_USERS_PATH=data/panel-users.json
```

Po pierwszym utworzeniu pliku kont hasło administratora jest przechowywane jako bcrypt; plik i katalog otrzymują uprawnienia ograniczone do właściciela. Zmiana hasła początkowego w zmiennych środowiskowych nie nadpisuje istniejącej bazy kont.

Role:
- **Podgląd (viewer):** odczyt danych i diagnostyki; żądania zmieniające stan są odrzucane przez serwer.
- **Operator (operator):** obsługa funkcji operacyjnych panelu; nie może zarządzać kontami panelu.
- **Administrator (admin):** pełny dostęp, w tym tworzenie kont, zmiana ról, blokowanie kont i reset haseł.

Sesja jest przechowywana w ciasteczku HttpOnly, SameSite=Strict; sesja przeglądarki trwa maksymalnie 8 godzin. Interfejs WWW i endpointy API wymagają zalogowania. Nie można zablokować ani zdegradować ostatniego aktywnego administratora. Konta panelu nie są kontami AlliedWare Plus/SSH.
