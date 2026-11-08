# ciscoswitch — notatki dla Claude Code

Panel WWW (Go) do zarządzania przełącznikiem **Allied Telesis AT-GS948MX** z **AlliedWare Plus 5.4.6**
(nie Cisco IOS — nazwa repo jest historyczna). Switch z rynku wtórnego, homelab, zresetowany do ustawień fabrycznych.
Panel zastępuje oryginalne GUI producenta, którego plik jest niedostępny.

## Struktura `internal/server`
- `server.go` — `Register` (wszystkie trasy API) i `write`.
- `config_backup.go` — eksport, porównanie, kopie konfiguracji, `runWithPreChangeBackup`.
- `ports.go`, `port_safety.go`, `port_profiles.go` — operacje na portach, porty chronione, profile.
- `topology.go` — parsery topologii (config, LLDP, LACP) i analiza ryzyka zmian.
- `security_audit.go`, `audit_history.go`, `diagnostics.go`, `monitor_history.go`.

## Komunikacja ze switchem
- SNMP (v2c lub v3) — stan portów z IF-MIB, `internal/device/device.go` (`readStatus`).
- SSH — polecenia `show` (lista dozwolonych: `allowed` w device.go) i zmiany konfiguracji (`runConfig`).
- Wydajność i cache: `internal/device/perf.go`.

## Zrobione (ostatnia zmiana: wydajność)
- SNMP GETBULK zamiast GETNEXT.
- Wspólne połączenie SSH dla odczytów (ponowne łączenie po zerwaniu / 45 s bezczynności).
- Cache SNMP i `show` (STATUS_CACHE_TTL / READ_CACHE_TTL, domyślnie 5 s), łączenie równoległych zapytań;
  cache czyszczony po każdej zmianie konfiguracji.
- `runConfig`: `terminal length 0` + `exit`, wykrywanie błędów CLI (`% ...`, bez `% Warning`).
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
4. Zrobione: gofmt i podział server.go. Dalej można wydzielić grupy tras z `Register` (ok. 750 linii).
5. SNMPv3: AES dostępny (`SWITCH_SNMP_V3_PRIV_PROTOCOL`, wybór w formularzu); potwierdzić, że AW+ 5.4.6 przyjmuje `priv aes`.

## Zasady
- Zmiany konfiguracji testować najpierw na nieużywanym porcie; nie dotykać portów z PROTECTED_PORTS.
- Nie commitować `.env` ani `data/` (hasła, community, kopie konfiguracji).
- Przed commitem: `gofmt -l .` (musi być puste), `go vet ./...`, `go test ./...`, `node --check web/app.js`.
- Zmiany w `.github/workflows` wymagają tokena z uprawnieniem Workflows.
