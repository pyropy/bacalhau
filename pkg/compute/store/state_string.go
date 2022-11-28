// Code generated by "stringer -type=ExecutionState --trimprefix=ExecutionState --output state_string.go"; DO NOT EDIT.

package store

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[ExecutionStateUndefined-0]
	_ = x[ExecutionStateCreated-1]
	_ = x[ExecutionStateBidAccepted-2]
	_ = x[ExecutionStateRunning-3]
	_ = x[ExecutionStateWaitingVerification-4]
	_ = x[ExecutionStateResultAccepted-5]
	_ = x[ExecutionStatePublishing-6]
	_ = x[ExecutionStateCompleted-7]
	_ = x[ExecutionStateFailed-8]
	_ = x[ExecutionStateCancelled-9]
}

const _ExecutionState_name = "UndefinedCreatedBidAcceptedRunningWaitingVerificationResultAcceptedPublishingCompletedFailedCancelled"

var _ExecutionState_index = [...]uint8{0, 9, 16, 27, 34, 53, 67, 77, 86, 92, 101}

func (i ExecutionState) String() string {
	if i < 0 || i >= ExecutionState(len(_ExecutionState_index)-1) {
		return "ExecutionState(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _ExecutionState_name[_ExecutionState_index[i]:_ExecutionState_index[i+1]]
}
