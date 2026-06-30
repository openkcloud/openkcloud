FROM alpine:3.18

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

COPY _output/bin/cost-based-scheduler /usr/local/bin/cost-based-scheduler

RUN chmod +x /usr/local/bin/cost-based-scheduler

ENTRYPOINT ["cost-based-scheduler"]
