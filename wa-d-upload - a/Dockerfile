# ---------- build stage ----------
FROM golang:1.24.5-alpine AS builder

# butuh gcc & sqlite-dev
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# compile dengan CGO_ENABLED=1 (default di Alpine sudah aktif karena pakai musl-gcc)
RUN CGO_ENABLED=1 GOOS=linux go build -a -o main .

# ---------- runtime stage ----------
FROM alpine:latest
RUN apk add --no-cache ca-certificates sqlite
WORKDIR /root/
COPY --from=builder /app/main .
EXPOSE 8080
CMD ["./main"]