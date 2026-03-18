FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod tidy

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mock-logto .

FROM scratch
COPY --from=builder /app/mock-logto /mock-logto
EXPOSE 9001
ENTRYPOINT ["/mock-logto"]
