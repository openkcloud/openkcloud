// ============================================================
// reacquire_test.go: 장치를 잃은 뒤 다시 잡는지 시험
// 상세: 드라이버 버전 전환으로 모듈이 내려가 /dev/rngd/* 가 사라지면 exporter 가 재기동한다.
// SMI 핸들을 기동 시 한 번만 만들면, 드라이버가 복귀해도 그 핸들은 계속 빈 값을 낸다.
// 생성일: 2026-08-10 | 수정일: 2026-10-13
// ============================================================
package main

import (
	"fmt"
	"testing"
	"time"
)

// perSourceFactory 는 **공급원 인스턴스마다 결과가 고정된** 가짜다.
//
// 실제 SMI 핸들이 그렇다. 장치가 없는 순간에 만든 핸들은 드라이버가 돌아와도 계속 빈 목록을
// 돌려준다 — 그래서 지표가 영구히 0건이 된다. 한 핸들을 계속 쓰는 경로에서는 결과가 절대
// 나아지지 않아야 하고, 새 세상을 보려면 재획득해야 한다.
type perSourceFactory struct {
	created int
	results []fakeSource // 생성 회차별 결과. 모자라면 마지막 것을 반복한다.
	newErrs []error      // 생성 회차별 오류. 범위를 벗어나면 오류 없음.
}

func (f *perSourceFactory) new() (DeviceSource, error) {
	i := f.created
	f.created++
	if i < len(f.newErrs) && f.newErrs[i] != nil {
		return nil, f.newErrs[i]
	}
	if len(f.results) == 0 {
		return fakeSource{}, nil
	}
	return f.results[min(i, len(f.results)-1)], nil
}

// mutableSource 는 한 핸들이 내는 값을 시험이 직접 바꾸는 가짜다. 핸들 교체 없이
// "돌던 장치가 사라졌다" 를 만들 때 쓴다.
type mutableSource struct{ cur *fakeSource }

func (m mutableSource) Devices() ([]DeviceReading, error) { return m.cur.readings, m.cur.err }

func liveDevice() []DeviceReading {
	return []DeviceReading{{
		Index: 0, Name: "npu0", BDF: "0000:27:00.0", CoreNum: 8,
		Cores: []CoreReading{{Core: 0, UsagePercent: 1}}, Alive: true,
	}}
}

// TestRefresherReacquiresAfterDeviceLoss 는 장치가 사라졌다 돌아오면 지표가 돌아오는지 본다.
//
// 재기동 시점에 장치가 없으면 첫 공급원이 빈 목록을 물고 굳어, 드라이버가 돌아온 뒤에도
// 그 핸들을 계속 쓰면 영원히 0건이 된다.
//
// 깨는 뮤테이션: refresher 가 공급원을 한 번만 만들고 재생성하지 않게 하면 실패한다.
func TestRefresherReacquiresAfterDeviceLoss(t *testing.T) {
	f := &perSourceFactory{results: []fakeSource{
		{},                       // 1회차 핸들: 장치 없음(드라이버 교체 중 기동)
		{readings: liveDevice()}, // 2회차 핸들: 드라이버 복귀
	}}
	r := newRefresher(f.new)

	r.refresh()
	if got := len(r.snapshot()); got != 0 {
		t.Fatalf("장치가 없는 회차의 샘플 수 = %d, 기대 0", got)
	}

	r.refresh()
	if got := len(r.snapshot()); got != 1 {
		t.Errorf("장치 복귀 후 샘플 수 = %d, 기대 1 — 공급원을 다시 만들지 않았다", got)
	}
	if f.created < 2 {
		t.Errorf("공급원 생성 횟수 = %d, 기대 2 이상 — 빈 회차 뒤 재획득을 시도하지 않았다", f.created)
	}
}

// TestRefresherKeepsSourceWhileDevicesPresent 는 잘 되는 동안에는 핸들을 재사용하는지 본다.
//
// 매 회차 재생성하면 SMI 초기화 비용을 주기마다 물고, Observer 가 시간 창으로 계산하는
// 코어 사용률이 매번 리셋돼 값이 늘 0 에 가깝게 나온다. 재획득은 잃었을 때만 한다.
//
// 깨는 뮤테이션: 성공 회차에도 공급원을 버리게 하면 생성 횟수가 늘어 실패한다.
func TestRefresherKeepsSourceWhileDevicesPresent(t *testing.T) {
	f := &perSourceFactory{results: []fakeSource{{readings: liveDevice()}}}
	r := newRefresher(f.new)

	for range 5 {
		r.refresh()
	}
	if f.created != 1 {
		t.Errorf("공급원 생성 횟수 = %d, 기대 1 — 잘 되는 동안에도 핸들을 버렸다", f.created)
	}
	if got := len(r.snapshot()); got != 1 {
		t.Errorf("샘플 수 = %d, 기대 1", got)
	}
}

// TestRefresherReacquiresAfterReadError 는 판독 오류 뒤에도 다시 잡는지 본다.
// 드라이버가 내려가면 빈 목록이 아니라 오류로 나올 수도 있다.
//
// 깨는 뮤테이션: 오류 회차에 공급원을 버리지 않게 하면 복귀 후에도 0건이라 실패한다.
func TestRefresherReacquiresAfterReadError(t *testing.T) {
	f := &perSourceFactory{results: []fakeSource{
		{err: errBoom},
		{readings: liveDevice()},
	}}
	r := newRefresher(f.new)

	r.refresh()
	if got := len(r.snapshot()); got != 0 {
		t.Fatalf("오류 회차의 샘플 수 = %d, 기대 0", got)
	}

	r.refresh()
	if got := len(r.snapshot()); got != 1 {
		t.Errorf("오류 뒤 복귀 샘플 수 = %d, 기대 1 — 오류 후 재획득을 안 했다", got)
	}
}

// TestRefresherSurvivesFactoryFailure 는 재획득 자체가 실패해도 죽지 않는지 본다.
// 드라이버가 아직 안 돌아온 동안에는 생성이 계속 실패한다 — 그때마다 죽으면 안 된다.
//
// 깨는 뮤테이션: 생성 실패를 log.Fatal 로 바꾸면 시험 프로세스가 죽어 실패한다.
func TestRefresherSurvivesFactoryFailure(t *testing.T) {
	f := &perSourceFactory{
		newErrs: []error{fmt.Errorf("SMI 초기화 실패"), nil},
		results: []fakeSource{{readings: liveDevice()}},
	}
	r := newRefresher(f.new)

	r.refresh() // 생성 실패 — 죽지 않고 넘어가야 한다
	if got := len(r.snapshot()); got != 0 {
		t.Fatalf("생성 실패 회차의 샘플 수 = %d, 기대 0", got)
	}

	r.refresh()
	if got := len(r.snapshot()); got != 1 {
		t.Errorf("생성 성공 후 샘플 수 = %d, 기대 1", got)
	}
}

// TestRefresherGivesUpAfterProlongedAbsence 는 장치를 오래 못 보면 프로세스를 끝내는지 본다.
// 장치가 없는 순간에 뜬 프로세스는 재시도로 복구되지 않는다 — 벤더 라이브러리 초기화가
// 프로세스 안에서 되돌려지지 않기 때문이다. 같은 설정의 새 프로세스는 즉시 정상화되므로
// 유일한 복구는 끝내고 kubelet 이 새로 띄우게 하는 것이다.
//
// 깨는 뮤테이션: giveUp 호출을 지우면 영원히 0건인 채로 살아남아 실패한다.
func TestRefresherGivesUpAfterProlongedAbsence(t *testing.T) {
	f := &perSourceFactory{results: []fakeSource{{}}} // 계속 빈 목록
	r := newRefresher(f.new)
	gaveUp := 0
	r.giveUp = func() { gaveUp++ }

	for range maxEmptyCycles - 1 {
		r.refresh()
	}
	if gaveUp != 0 {
		t.Fatalf("아직 %d 회차인데 포기했다 — 정상 드라이버 교체 중에 프로세스가 죽는다", maxEmptyCycles-1)
	}

	r.refresh()
	if gaveUp != 1 {
		t.Errorf("%d 회차 연속 빈 목록인데 포기하지 않았다 — 영구히 0건인 채로 남는다", maxEmptyCycles)
	}
}

// TestRefresherResetsAbsenceCountOnRecovery 는 한 번이라도 장치를 보면 카운터가 풀리는지 본다.
// 안 풀면 간헐적 실패가 쌓여 멀쩡한 프로세스가 죽는다.
//
// 깨는 뮤테이션: `r.empty = 0` 을 지우면 누적되어 포기해 실패한다.
func TestRefresherResetsAbsenceCountOnRecovery(t *testing.T) {
	cur := fakeSource{}
	r := newRefresher(func() (DeviceSource, error) { return mutableSource{cur: &cur}, nil })
	gaveUp := 0
	r.giveUp = func() { gaveUp++ }

	for range maxEmptyCycles - 1 {
		r.refresh()
	}
	cur = fakeSource{readings: liveDevice()} // 장치 복귀
	r.refresh()
	cur = fakeSource{} // 다시 사라짐
	for range maxEmptyCycles - 1 {
		r.refresh()
	}
	if gaveUp != 0 {
		t.Errorf("복귀로 카운터가 풀리지 않아 포기했다 — 간헐적 실패가 누적된다")
	}
}

// TestRefresherDropsStaleSnapshotWhenDevicesVanish 는 장치를 못 읽은 회차에 마지막 정상값을 내보내지 않는지 본다.
//
// 붙들고 있으면 장치가 죽은 동안에도 furiosa_npu_alive 가 1 을 보고한다 — 없는 것을
// 있다고 말하는 쪽이, 지표가 잠깐 비어 absent() 로 잡히는 쪽보다 나쁘다.
//
// 깨는 뮤테이션: publish 를 `if len(samples) > 0` 로 감싸면 낡은 값이 남아 실패한다.
func TestRefresherDropsStaleSnapshotWhenDevicesVanish(t *testing.T) {
	for _, c := range []struct {
		name   string
		second fakeSource
	}{
		{"장치 목록이 빈 회차", fakeSource{}},
		{"판독이 실패한 회차", fakeSource{err: errBoom}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cur := fakeSource{readings: liveDevice()}
			r := newRefresher(func() (DeviceSource, error) { return mutableSource{cur: &cur}, nil })

			r.refresh()
			if got := len(r.snapshot()); got != 1 {
				t.Fatalf("첫 회차 샘플 수 = %d, 기대 1", got)
			}

			cur = c.second // 같은 핸들이 내는 값이 바뀐다
			r.refresh()
			if got := len(r.snapshot()); got != 0 {
				t.Errorf("장치를 못 읽은 회차의 샘플 수 = %d, 기대 0 — 낡은 값을 계속 내보낸다", got)
			}
		})
	}
}

// TestMaxEmptyCyclesClearsObservedSwapWindow 는 임계값이 정상 드라이버 교체 구간보다 넉넉한지 본다.
//
// 다른 임계값 시험은 상수를 따라가므로 낮춰도 통과한다. 이 시험만 상수를 실측 교체
// 구간(약 50초)과 직접 대조해, 너무 낮춰 exporter 가 스스로 죽는 회귀를 잡는다.
//
// 깨는 뮤테이션: maxEmptyCycles 를 5 이하로 낮추면 실패한다.
func TestMaxEmptyCyclesClearsObservedSwapWindow(t *testing.T) {
	const (
		defaultInterval   = 10 * time.Second // main.go 의 -interval 기본값
		observedSwapBlind = 50 * time.Second // 정상 드라이버 교체 중 실측
	)
	tolerated := time.Duration(maxEmptyCycles) * defaultInterval
	if tolerated <= observedSwapBlind {
		t.Errorf("장치 부재를 %s 만 견딘다 — 관측된 교체 구간 %s 보다 짧아 정상 교체 중에 프로세스가 죽는다",
			tolerated, observedSwapBlind)
	}
}
