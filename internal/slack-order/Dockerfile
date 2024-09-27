FROM golang:1.23-alpine as build
LABEL org.opencontainers.image.source=https://github.com/kndpio/function-poll

WORKDIR /build
COPY ./internal/slack-order .
RUN go mod tidy
RUN go build -o slack-order

FROM golang:1.23-alpine
WORKDIR /app
COPY --from=build /build/slack-order /app/
CMD [ "./slack-order" ]
