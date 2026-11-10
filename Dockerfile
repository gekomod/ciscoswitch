# syntax=docker/dockerfile:1
# Obraz panelu GS948MX. Budowa: docker build -t gs948mx-manager .

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go mod tidy \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/gs948mx-manager .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S panel && adduser -S -G panel -h /app panel
WORKDIR /app
COPY --from=build /out/gs948mx-manager /app/gs948mx-manager
COPY web /app/web
RUN mkdir -p /app/data && chown -R panel:panel /app/data
USER panel

# W kontenerze panel musi nasłuchiwać na wszystkich interfejsach;
# dostęp ogranicz publikując port tylko na wybranym adresie (zob. docker-compose.yml).
ENV LISTEN_ADDR=0.0.0.0:8090 \
    TZ=Europe/Warsaw
VOLUME ["/app/data"]
EXPOSE 8090 8091

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8090/login || exit 1

ENTRYPOINT ["/app/gs948mx-manager"]
