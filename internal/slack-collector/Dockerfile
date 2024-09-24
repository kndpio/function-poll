FROM golang:1.23-alpine as build
LABEL org.opencontainers.image.source=https://github.com/kndpio/function-poll

WORKDIR /build
COPY ./internal/slack-collector .
RUN go mod tidy
RUN go build -o slack-collector

FROM golang:1.23-alpine
WORKDIR /app
COPY --from=build /build/slack-collector /app/
EXPOSE 3000
CMD [ "./slack-collector" ]
