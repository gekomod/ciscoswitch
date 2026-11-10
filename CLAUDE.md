# ciscoswitch — notatki dla Claude Code

Panel WWW (Go) do zarządzania przełącznikiem **Allied Telesis AT-GS948MX** z **AlliedWare Plus 5.4.6**
(nie Cisco IOS — nazwa repo jest historyczna). Switch z rynku wtórnego, homelab, zresetowany do ustawień fabrycznych.
Panel zastępuje oryginalne GUI producenta, którego plik jest niedostępny.

## Struktura `internal/server`
- `server.go` — `Register`: trasy odczytu i wywołania grup tras; `write`.
- `routes_config.go` — trasy zmian: podglądy, ustawienia urządzenia, konfiguracja portów, narzędzia (zapis, CLI).
- `config_backup.go` — eksport, porównanie, kopie konfiguracji, `runWithPreChangeBackup`.
- `ports.go`, `port_safety.go`, `port_profiles.go` — operacje na portach, porty chronione, profile.
- `topology.go` — parsery topologii (config, LLDP, LACP) i analiza ryzyka zmian.
- `security_audit.go`, `audit_history.go`, `diagnostics.go`, `monitor_history.go`.

## Frontend (`web/`)
- `loading.js` — przechwytuje `fetch` do `/api/` po kliknięciu: pasek postępu, przycisk zajęty, komunikaty. Ładowany przed `app.js`.
- `faceplate.js` — front switcha na przeglądzie; korzysta z `latestPorts`, zdarzenia `ports:updated` (wysyłanego w `load()`), `openPortDialog`, `openPortDiagnostics`, `activatePage`.
- Menu: grupy Przegląd, Porty, Przełączanie, Sieć, System, Narzędzia (makieta: artefakt „Panel AT-GS948MX”).
- `files.js` + `internal/device/files.go` + `internal/server/files_routes.go` — pliki we flash i ustawienia startu. Formaty `dir` i `show boot` niepotwierdzone na GS948MX.
- `monitoring.js` + `internal/device/monitoring.go` + `internal/server/monitoring_routes.go` — logi, czas/NTP, mirroring (stan z running-config). Do potwierdzenia: format `show log`, składnia `clock timezone` i `mirror interface` na 5.4.6; brak czasu letniego.
- `traffic.js` + `internal/device/traffic.go` + `internal/server/traffic_routes.go` — pętle, storm control, IGMP. Brak QoS.
- `NormalizeInterfaceRanges` (traffic.go): AW+ zwija porty w zakresy `interface port1.0.1-1.0.24`; każdy nowy parser running-config musi użyć tej funkcji albo `InterfaceBlocks`.
- `security.js` + `internal/device/security.go` + `internal/server/security_routes.go` — port security, DHCP snooping, ACL; blokady w `securityPortsAllowed` i w Build*/Configure* (zaufane porty, ostatnia reguła ACL).
- Raport zgodności: `internal/device/compat.go`, `web/compat.js`, `/api/compat`. Pierwszy krok przy problemach na prawdziwym switchu: poprosić użytkownika o ten raport.
- Firmware: `internal/server/firmware.go` (upload, serveOnce z tokenem, zadanie w tle), `CopyFirmwareFromURL` w files.go; składnia `copy http://... flash:/x.rel` niepotwierdzona na 5.4.6.
- QoS podstawowy: `internal/device/qos.go`, `web/qos.js`, akcje qos-global/qos-port w `/api/traffic`, odczyt `/api/qos`. Brak map klas/polityk.
- Do zrobienia: potwierdzenie składni wszystkich modułów na prawdziwym AW+ 5.4.6.
- `services.js` + `internal/device/services.go` + `internal/server/services_routes.go` — moduł usług (SSH zablokowane, SNMP/DHCP wymagają potwierdzenia).
- Domyślne stany usług AW+ nie są pewne; stan „nieustalony” jest celowy. Potwierdzić na switchu formaty `show http`, `show telnet`, `show lldp`.

## Komunikacja ze switchem
- SNMP (v2c lub v3) — stan portów z IF-MIB, `internal/device/device.go` (`readStatus`).
- SSH — polecenia `show` (lista dozwolonych: `allowed` w device.go) i zmiany konfiguracji (`runConfig`).
- Wydajność i cache: `internal/device/perf.go`.

## Zrobione (ostatnia zmiana: wydajność)
- SNMP GETBULK zamiast GETNEXT.
- Wspólne połączenie SSH dla odczytów (ponowne łączenie po zerwaniu / 45 s bezczynności).
- Cache SNMP i `show` (STATUS_CACHE_TTL / READ_CACHE_TTL, domyślnie 5 s), łączenie równoległych zapytań;
  cache czyszczony po każdej zmianie konfiguracji.
- `runConfig`: `enable` (AW+ loguje do trybu awplus>) + `terminal length 0` + `exit`, wykrywanie błędów CLI (`% ...`, bez `% Warning`).
- `PERF_LOG=1` loguje czas każdego zapytania (`[perf] ...`).

## Parsery a prawdziwe wyjścia AW+
- `internal/device/testdata/awplus/` — prawdziwe wyjścia AW+ 5.4.x z ntc-templates (Apache 2.0); testy w `awplus_samples_test.go`.
- Naprawione: zawijane listy "Configured Vlans", statyczne LAG (`static-channel-group N` → `saN`, wcześniej w ogóle niewykrywane),
  VLAN w tablicy MAC (kolumny VLAN, port, MAC), wykrywanie błędów CLI (AW+ zaczyna od "% " także linie informacyjne).
- Wpisy MAC i członkostwo VLAN za LAG (`saN`, `poN`) są widoczne w topologii jako osobne wiersze.
- Brak próbki `show lldp neighbors` (skrót) i `show spanning-tree` — format niepotwierdzony; pobrać z własnego switcha.

## Do zweryfikowania na prawdziwym switchu
- Czy AW+ 5.4.6 poprawnie kończy sesję po `exit` i akceptuje `terminal length 0`.
- Czy współdzielone połączenie SSH jest stabilne (limity sesji AW+).
- Logi `[perf]` po przeklikaniu panelu — co jest jeszcze wolne.

## Dalszy plan
1. Pomiary `[perf]` na żywym switchu.
2. Jeden odczyt `show running-config` na operację zmiany portu (powtórzone odczyty obsługuje już cache 5 s).
3. Dopasowanie parserów (LACP, STP, LLDP, MAC, VLAN) do prawdziwych wyjść z AW+ 5.4.6 + testy na tych wyjściach.
4. Zrobione: gofmt, podział server.go, wydzielenie grup tras z `Register` (server.go: 132 linie).
5. SNMPv3: AES dostępny (`SWITCH_SNMP_V3_PRIV_PROTOCOL`, wybór w formularzu); potwierdzić, że AW+ 5.4.6 przyjmuje `priv aes`.

## Ustawienia
- `internal/settings`: `settings.Get(KEY)` = panel (data/settings.json) → .env → domyślna. Nowe ustawienia nie-sekretne dodawać do `settings.Defs` i czytać przez `settings.Get`, nie `os.Getenv`. Hasła tylko w .env. Zmiany w locie przez `settings.OnChange` (main.go → `svc.SetTuning`). `/api/settings` tylko dla admina (middleware).

## Zasady
- Zmiany konfiguracji testować najpierw na nieużywanym porcie; nie dotykać portów z PROTECTED_PORTS.
- Nie commitować `.env` ani `data/` (hasła, community, kopie konfiguracji).
- Przed commitem: `gofmt -l .` (musi być puste), `go vet ./...`, `go test ./...`, `node --check web/app.js`.
- Zmiany w `.github/workflows` wymagają tokena z uprawnieniem Workflows.
