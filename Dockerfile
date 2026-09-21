FROM m.daocloud.io/docker.io/library/golang:1.26.4-alpine@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTELEMETRY=off
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd ./cmd
COPY internal ./internal
RUN go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/jev-mcphub ./cmd/jev-mcphub

FROM m.daocloud.io/gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/jev-mcphub /jev-mcphub
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/jev-mcphub", "healthcheck"]
ENTRYPOINT ["/jev-mcphub"]
CMD ["serve"]
