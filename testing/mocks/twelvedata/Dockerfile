FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod tidy

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mock-twelvedata .

FROM scratch
COPY --from=builder /app/mock-twelvedata /mock-twelvedata
ENV REST_PORT=9002 WS_PORT=9003
EXPOSE 9002 9003
ENTRYPOINT ["/mock-twelvedata"]
