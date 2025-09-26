FROM golang:1.25 as build-image
WORKDIR /src
COPY go.mod go.sum main.go ./
RUN go build -o lambda-handler
FROM public.ecr.aws/lambda/provided:al2023
WORKDIR /var/task
COPY --from=build-image /src/lambda-handler .
COPY .env ./
ENTRYPOINT ./lambda-handler
