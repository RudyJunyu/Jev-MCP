FROM golang:1.26.4-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTELEMETRY=off
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd ./cmd
COPY internal ./internal
RUN go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/jev-mcphub ./cmd/jev-mcphub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/jev-mcphub /jev-mcphub
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/jev-mcphub", "healthcheck"]
ENTRYPOINT ["/jev-mcphub"]
CMD ["serve"]
