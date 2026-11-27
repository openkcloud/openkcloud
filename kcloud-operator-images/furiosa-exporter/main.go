// ============================================================
// main.go: Furiosa exporter 진입점. 주기 수집 + :9410/metrics 서빙.
// 상세: 수집을 goroutine 이 주기로 돌려 스냅샷을 갱신하고, /metrics 요청은 그 스냅샷만
// 읽는다 — 스크레이프가 벤더 SDK 호출을 직접 유발해 폭주로 장치를 때리는 것을 막는다.
// --kube-resources-label 을 켜면 같은 주기로 kubelet 할당 기록도 다시 읽는다.
// 생성일: 2026-08-07 | 수정일: 2026-10-15
// ============================================================
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

func main() {
	listen := flag.String("listen", ":9410", "metrics 리슨 주소")
	interval := flag.Duration("interval", 10*time.Second, "장치 수집 주기")
	kubeResourcesLabel := flag.Bool("kube-resources-label", false,
		"kubelet 할당 기록(PodResources)으로 장치를 쓰는 컨테이너의 namespace·pod·container 라벨을 붙인다")
	flag.Parse()

	node := os.Getenv("NODE_NAME")
	if node == "" {
		log.Fatal("NODE_NAME 환경변수가 비어 있다 — DaemonSet 이 fieldRef 로 주입해야 한다")
	}

	labels, src := loadThrottleLabels(throttleReasonListGlob)
	throttleLabels = labels
	if src == "" {
		src = "furiosa-smi 헤더 기본값"
	}
	log.Printf("쓰로틀 사유 비트표 %d종 (%s)", len(throttleLabels), src)

	// 공급원 생성은 refresher 에 맡긴다. 여기서 만들어 실패 시 종료하면, 드라이버 교체로 장치가
	// 잠깐 사라진 순간에 뜬 프로세스가 재기동 루프에 빠지거나 빈 핸들을 물고 굳는다.
	// 잃으면 다시 잡는 것이 이 프로세스의 일이다.
	r := newRefresher(NewSMISource)
	r.refresh()
	var owners *ownerTable
	if *kubeResourcesLabel {
		owners = &ownerTable{list: func(ctx context.Context) (*podresourcesv1.ListPodResourcesResponse, error) {
			return listPodResources(ctx, podResourcesSocket)
		}}
		owners.refresh(context.Background())
		owners.warnUnmatched(r.snapshot())
	}
	go func() {
		t := time.NewTicker(*interval)
		defer t.Stop()
		for range t.C {
			r.refresh()
			// 드라이버를 다른 판본으로 바꿔도 장치가 돌아오면 프로세스는 계속 산다. 사유 이름이 옛 표로
			// 남지 않게, 표가 바뀌면 끝내서 kubelet 재기동으로 새 표를 읽는다.
			if throttleTableChanged(throttleReasonListGlob, throttleLabels) {
				log.Fatalf("노드 드라이버의 쓰로틀 사유 비트표가 바뀌었다 — 새 표로 다시 시작한다")
			}
			if owners != nil {
				owners.refresh(context.Background())
				owners.warnUnmatched(r.snapshot())
			}
		}
	}()
	// 쓰로틀 사유는 수집 주기와 별개로 1초마다 읽어 변화를 센다.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for now := range t.C {
			r.sampleThrottle(now)
		}
	}()

	m := &Metrics{Node: node, Samples: r.snapshot,
		Throttle: func(uuid string) (map[string]float64, bool) { return r.throttle.counts(uuid, time.Now()) }}
	if owners != nil {
		m.Owner = owners.owner
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("furiosa-exporter listening on %s (node=%s, interval=%s, kube-resources-label=%t)",
		*listen, node, *interval, *kubeResourcesLabel)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
