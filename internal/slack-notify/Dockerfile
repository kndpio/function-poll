FROM golang:1.23-alpine as build
LABEL org.opencontainers.image.source=https://github.com/kndpio/function-poll

WORKDIR /build
COPY ./internal/slack-notify .
RUN go mod tidy
RUN go build -o slack-notify

FROM golang:1.23-alpine
WORKDIR /app
COPY --from=build /build/slack-notify /app/
CMD [ "./slack-notify" ]
