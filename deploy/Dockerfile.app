FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fragnight ./cmd/fragnight && mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/fragnight /fragnight
COPY LICENSE THIRD_PARTY_NOTICES.md /licenses/
# A named volume mounted here inherits this owner, so the non-root user can write the database.
COPY --from=build --chown=nonroot:nonroot /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/fragnight"]
