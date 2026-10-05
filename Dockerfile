# syntax=docker/dockerfile:1

# ---------- build ----------
FROM golang:1.26-alpine AS build

RUN apk add --no-cache ca-certificates

WORKDIR /src

# Capa cacheada: solo se rehace si cambian las dependencias. El comodín
# admite que go.sum aún no exista mientras no haya dependencias externas.
COPY go.* ./
RUN go mod download

# templates/ y static/ van embebidos en el binario con //go:embed
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /simpleebay .

# ---------- runtime ----------
FROM scratch

# Necesarios para el TLS de salida hacia la API de eBay
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /simpleebay /simpleebay

USER 65534:65534
EXPOSE 8080

ENTRYPOINT ["/simpleebay"]
