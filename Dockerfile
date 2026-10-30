FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/myplatform .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S myplatform \
    && adduser -S -G myplatform myplatform \
    && mkdir -p /data \
    && chown myplatform:myplatform /data

WORKDIR /app
COPY --from=build /out/myplatform ./myplatform
ENV HOME=/data
USER myplatform
EXPOSE 8080
ENTRYPOINT ["./myplatform"]
CMD ["web", "--addr", "0.0.0.0:8080"]
