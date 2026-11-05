# CiscoSwitch Control Center — Allied Telesis AT-GS948MX

Nowoczesny panel WWW dla przełącznika AT-GS948MX z AlliedWare Plus. Nazwa repozytorium ciscoswitch jest nazwą projektu, ale obecny adapter CLI jest przeznaczony dla Allied Telesis, a nie Cisco IOS.

## Funkcje
- Odczyt portów przez SNMP v2c / IF-MIB, naturalne sortowanie i filtr.
- Włączanie i wyłączanie portu przez SSH.
- Włączanie i wyłączanie klienta DHCP na interfejsie zarządzającym VLAN 1.
- Ręczny zapis running-config do startup-config.
- Diagnostyczne polecenia show z zamkniętej listy.
- Responsywny panel WWW, potwierdzenia przed zmianami.

## Instalacja na Debianie 13

```bash
git clone https://github.com/gekomod/ciscoswitch.git
cd ciscoswitch
cp .env.example .env
nano .env
go mod tidy
go run .
```

Domyślnie panel słucha na 127.0.0.1:8090.

## .env

```dotenv
SWITCH_HOST=192.168.31.2
SWITCH_SSH_PORT=22
SWITCH_SSH_USER=manager
SWITCH_SSH_PASSWORD=CHANGE_ME
SWITCH_SNMP_COMMUNITY=CHANGE_ME_READONLY
SWITCH_SNMP_PORT=161
LISTEN_ADDR=127.0.0.1:8090
```

## Ważne ograniczenia
- DHCP oznacza klienta DHCP dla VLAN 1, nie serwer DHCP obsługujący komputery.
- Zmiana DHCP może zmienić adres IP switcha i przerwać połączenie.
- Testuj zmianę portu najpierw na porcie niekrytycznym.
- Interaktywne CLI i komendy konfiguracyjne wymagają potwierdzenia na dokładnym firmware przed wdrożeniem produkcyjnym.
- SSH dopuszcza legacy ssh-rsa i nie weryfikuje klucza hosta. Panel domyślnie nasłuchuje wyłącznie na localhost.
- Nie commituj .env, haseł ani community SNMP.

## Kolejne moduły
VLAN access/trunk, opis portu, prędkość/duplex, DHCP server/pools, routing, ACL, STP, LACP i pozostałe opcje wymagają osobnej implementacji i weryfikacji składni dla konkretnego modelu/firmware. Panel nie udostępnia dowolnego terminala.
