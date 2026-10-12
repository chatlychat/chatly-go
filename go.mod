// The published import path is github.com/chatlychat/chatly-go; the source
// of truth lives in the Chatly monorepo at packages/sdk-go and is mirrored
// out to that repository.
//
// Zero third-party requires, on purpose: a customer should not inherit our
// dependency tree to send a message. There is no `require` block below, and
// there must never be one — that is the whole promise of this module.
module github.com/chatlychat/chatly-go

// 1.23 is the floor because Pager.Seq returns an iter.Seq2 (range-over-func),
// which landed in 1.23. Nothing here needs anything newer.
go 1.23.0
