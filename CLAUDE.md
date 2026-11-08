# ciscoswitch — notatki dla Claude Code

Panel WWW (Go) do zarządzania przełącznikiem **Allied Telesis AT-GS948MX** z **AlliedWare Plus 5.4.6**
(nie Cisco IOS — nazwa repo jest historyczna). Switch z rynku wtórnego, homelab, zresetowany do ustawień fabrycznych.
Panel zastępuje oryginalne GUI producenta, którego plik jest niedostępny.

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

## Do zweryfikowania na prawdziwym switchu
- Czy AW+ 5.4.6 poprawnie kończy sesję po `exit` i akceptuje `terminal length 0`.
- Czy współdzielone połączenie SSH jest stabilne (limity sesji AW+).
- Logi `[perf]` po przeklikaniu panelu — co jest jeszcze wolne.

## Dalszy plan
1. Pomiary `[perf]` na żywym switchu.
2. Jeden odczyt `show running-config` na operację zmiany portu (teraz kilka, częściowo łagodzone przez cache).
3. Dopasowanie parserów (LACP, STP, LLDP, MAC, VLAN) do prawdziwych wyjść z AW+ 5.4.6 + testy na tych wyjściach.
4. Rozbicie długich jednolinijkowych handlerów w `internal/server/server.go`.
5. SNMPv3: rozważyć AES zamiast DES, jeśli firmware obsługuje.

## Zasady
- Zmiany konfiguracji testować najpierw na nieużywanym porcie; nie dotykać portów z PROTECTED_PORTS.
- Nie commitować `.env` ani `data/` (hasła, community, kopie konfiguracji).
- Przed commitem: `go vet ./...` i `go test ./...`.
