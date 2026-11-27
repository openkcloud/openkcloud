// backend_test.go: Backend 인터페이스 계약 컴파일 검증
package partition

import "testing"

// fakeBackend 는 인터페이스 만족을 컴파일 타임에 강제한다.
type fakeBackend struct{}

func (fakeBackend) Vendor() string                             { return "fake" }
func (fakeBackend) Discover(t Target) (*DiscoverResult, error) { return &DiscoverResult{}, nil }
func (fakeBackend) Validate(layout []Layout) error             { return nil }
func (fakeBackend) Diff(t Target, resolved []ResolvedEntry) (DiffResult, error) {
	return DiffResult{}, nil
}
func (fakeBackend) Apply(t Target, resolved []ResolvedEntry) (*RollbackState, error) {
	return nil, ErrUnsupported
}
func (fakeBackend) Verify(t Target) (*VerifyResult, error)   { return &VerifyResult{}, nil }
func (fakeBackend) Rollback(t Target, s RollbackState) error { return ErrUnsupported }

func TestBackendInterfaceSatisfied(t *testing.T) {
	var _ Backend = fakeBackend{}
}
