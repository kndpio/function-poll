FROM golang:1.20-alpine as build
LABEL org.opencontainers.image.source https://github.com/kndpio/kndp

WORKDIR /build
COPY ./packages/meal/slack-collector .
RUN go mod tidy
RUN go build -o slack-collector

FROM golang:1.20-alpine
WORKDIR /app
COPY --from=build /build/slack-collector /app/
EXPOSE 3000
CMD [ "./slack-collector" ]
