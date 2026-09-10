FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY backend ./backend
ARG TARGET=otklik
RUN go test ./backend/... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./backend/cmd/${TARGET}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
