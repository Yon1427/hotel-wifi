FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/admin ./cmd/admin

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=build /out/admin /app/admin
COPY web /app/web
USER app
EXPOSE 8080
ENTRYPOINT ["/app/admin"]
