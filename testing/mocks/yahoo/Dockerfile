FROM golang:1.21-alpine AS builder
RUN apk add --no-cache ca-certificates curl

WORKDIR /app
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mock-yahoo .

FROM scratch
COPY --from=builder /app/mock-yahoo /mock-yahoo
EXPOSE 9005
ENTRYPOINT ["/mock-yahoo"]
