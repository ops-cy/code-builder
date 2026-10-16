package webhook

import "testing"

func Test_branch_when_BranchRef_thenReturnName(t *testing.T) {
	ev := PushEvent{Ref: "refs/heads/main"}
	b, ok := ev.branch()
	if !ok || b != "main" {
		t.Errorf("branch() = (%q, %v), want (main, true)", b, ok)
	}
}

func Test_branch_when_TagRef_thenReturnNotOK(t *testing.T) {
	ev := PushEvent{Ref: "refs/tags/v1.0"}
	if _, ok := ev.branch(); ok {
		t.Error("branch() on tag ref should be not ok")
	}
}

func Test_sha_when_CheckoutSHAPresent_thenReturnIt(t *testing.T) {
	ev := PushEvent{After: "bbbb", CheckoutSHA: "aaaa"}
	if got := ev.sha(); got != "aaaa" {
		t.Errorf("sha() = %q, want checkout_sha", got)
	}
}

func Test_sha_when_CheckoutSHAEmpty_thenReturnAfter(t *testing.T) {
	ev := PushEvent{After: "bbbb", CheckoutSHA: ""}
	if got := ev.sha(); got != "bbbb" {
		t.Errorf("sha() = %q, want after", got)
	}
}

func Test_isDelete_when_AllZeroAfterAndNoCheckoutSHA_thenReturnTrue(t *testing.T) {
	ev := PushEvent{After: "0000000000000000000000000000000000000000", CheckoutSHA: ""}
	if !ev.isDelete() {
		t.Error("isDelete() = false for all-zero after with empty checkout_sha")
	}
}

func Test_isDelete_when_RealSHA_thenReturnFalse(t *testing.T) {
	ev := PushEvent{After: "0000000000000000000000000000000000000abc", CheckoutSHA: "abc"}
	if ev.isDelete() {
		t.Error("isDelete() = true for real sha")
	}
}

func Test_isPush_when_ObjectKindPush_thenReturnTrue(t *testing.T) {
	if !(PushEvent{ObjectKind: "push"}).isPush() {
		t.Error("isPush() = false for object_kind push")
	}
	if (PushEvent{ObjectKind: "tag_push", EventName: "tag_push"}).isPush() {
		t.Error("isPush() = true for tag_push")
	}
}
