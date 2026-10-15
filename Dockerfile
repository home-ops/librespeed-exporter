FROM --platform=$BUILDPLATFORM golang:1.25.8-bookworm@sha256:4557cf171e3cdf5053a298d5171b1a5f5734d920260c25f22c79e94760eb2084 AS build

ARG LIBRESPEED_CLI_VERSION=v1.0.14
ARG LIBRESPEED_CLI_COMMIT=42e5f76c90e64e04ab7079fd76d833dfe41805da
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src/exporter
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags='-s -w' -o /out/librespeed-exporter ./cmd/librespeed-exporter

RUN git init /src/librespeed-cli \
	&& git -C /src/librespeed-cli remote add origin https://github.com/librespeed/speedtest-cli.git \
	&& git -C /src/librespeed-cli fetch --depth=1 origin \
		"refs/tags/${LIBRESPEED_CLI_VERSION}:refs/tags/${LIBRESPEED_CLI_VERSION}" \
	&& test "$(git -C /src/librespeed-cli rev-list -n 1 ${LIBRESPEED_CLI_VERSION})" = "${LIBRESPEED_CLI_COMMIT}" \
	&& git -C /src/librespeed-cli checkout --detach "${LIBRESPEED_CLI_COMMIT}" \
	&& test "$(git -C /src/librespeed-cli rev-parse HEAD)" = "${LIBRESPEED_CLI_COMMIT}" \
	&& test "$(git -C /src/librespeed-cli rev-list -n 1 ${LIBRESPEED_CLI_VERSION})" = "${LIBRESPEED_CLI_COMMIT}"

RUN cd /src/librespeed-cli \
	&& CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags='-s -w' -o /out/librespeed-cli .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

ARG VERSION=0.1.0
ARG CREATED=1970-01-01T00:00:00Z
LABEL org.opencontainers.image.title="LibreSpeed Exporter" \
	org.opencontainers.image.description="Scheduled LibreSpeed tests exported as Prometheus metrics" \
	org.opencontainers.image.source="https://github.com/home-ops/librespeed-exporter" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.created="${CREATED}" \
	org.opencontainers.image.licenses="Apache-2.0 AND LGPL-3.0-only AND GPL-3.0-only"

WORKDIR /etc/librespeed
COPY --from=build /out/librespeed-exporter /usr/local/bin/librespeed-exporter
COPY --from=build /out/librespeed-cli /usr/local/bin/librespeed-cli
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY LICENSE /licenses/LICENSE
COPY LICENSES/GPL-3.0.txt /licenses/GPL-3.0.txt
COPY LICENSES/librespeed-cli-LGPL-3.0.txt /licenses/librespeed-cli-LGPL-3.0.txt
COPY LICENSES/GO-MODULES/ /licenses/GO-MODULES/
COPY THIRD_PARTY.md /licenses/THIRD_PARTY.md

USER 65532:65532
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/librespeed-exporter"]
