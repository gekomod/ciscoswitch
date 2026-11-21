#!/bin/sh
# Buduje web/index.html z komponentów gtml (ui/components + ui/routes).
# Wymaga gtml: go install github.com/phillip-england/gtml@latest
# Użycie: ./ui/build.sh          jednorazowo
#         ./ui/build.sh --watch  przebudowa po każdej zmianie komponentu
set -eu
UI_DIR=$(cd "$(dirname "$0")" && pwd)
WEB_DIR="$UI_DIR/../web"
command -v gtml >/dev/null 2>&1 || { echo "Brak gtml. Zainstaluj: go install github.com/phillip-england/gtml@latest" >&2; exit 1; }
build() {
  gtml compile "$UI_DIR"
  cp "$UI_DIR/dist/index.html" "$WEB_DIR/index.html"
  echo "web/index.html zaktualizowany"
}
build
if [ "${1:-}" = "--watch" ]; then
  # gtml --watch nie kopiuje wyniku do web/, więc pilnujemy zmian sami (bez dodatkowych zależności).
  last=""
  while sleep 1; do
    now=$(find "$UI_DIR/components" "$UI_DIR/routes" -type f -newer "$WEB_DIR/index.html" | head -1)
    [ -n "$now" ] && [ "$now" != "$last" ] && { build || true; last=""; }
  done
fi
