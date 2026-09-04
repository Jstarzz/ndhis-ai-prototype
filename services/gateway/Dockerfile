FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway .

FROM alpine:3.21
RUN adduser -D -u 10001 app
USER app
COPY --from=build /gateway /gateway
EXPOSE 8080
ENTRYPOINT ["/gateway"]
