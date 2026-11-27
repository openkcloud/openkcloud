FROM alpine:3.18

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

COPY _output/bin/keti-cost-scheduler /usr/local/bin/keti-cost-scheduler

RUN chmod +x /usr/local/bin/keti-cost-scheduler

ENTRYPOINT ["keti-cost-scheduler"]
