package main

import "context"

// webDeleteConversationRPC deletes a conversation, as the web app's chat list
// does.
const webDeleteConversationRPC = "GzXR5e"

func (session *webSession) deleteConversation(ctx context.Context, conversation string) error {
	return session.rpcAck(ctx, webDeleteConversationRPC, []any{conversation})
}

// deleteChat removes a conversation a turn no longer needs. A failure is
// reported rather than returned: the turn has already produced its answer.
func (service *service) deleteChat(ctx context.Context, session *webSession, conversation string) {
	if conversation == "" {
		return
	}
	if err := session.deleteConversation(ctx, conversation); err != nil {
		service.report(map[string]any{"provider": provider, "state": "chat_delete_failed", "reason": safeCredentialCode(err)}, "gemini-web: a generated chat could not be deleted")
	}
}
