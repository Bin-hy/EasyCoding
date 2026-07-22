package conversation

import "testing"

func TestConversation_AddAndMessages(t *testing.T) {
	c := &Conversation{}
	c.AddUser("hello")
	c.AddAssistant("hi there")
	c.AddUser("how are you?")

	msgs := c.Messages()
	if len(msgs) != 3 {
		t.Fatalf("期望 3 条消息，实际 %d", len(msgs))
	}

	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("第 1 条期望 user/hello，实际 %s/%s", msgs[0].Role, msgs[0].Content)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("第 2 条期望 assistant/hi there，实际 %s/%s", msgs[1].Role, msgs[1].Content)
	}
	if msgs[2].Role != "user" || msgs[2].Content != "how are you?" {
		t.Errorf("第 3 条期望 user/how are you?，实际 %s/%s", msgs[2].Role, msgs[2].Content)
	}

	// 验证 Messages() 返回的是副本
	msgs[0].Content = "modified"
	if c.messages[0].Content != "hello" {
		t.Error("Messages() 应该返回副本，不应影响内部状态")
	}
}

func TestConversation_Empty(t *testing.T) {
	c := &Conversation{}
	msgs := c.Messages()
	if len(msgs) != 0 {
		t.Errorf("空对话期望 0 条消息，实际 %d", len(msgs))
	}
}

func TestConversation_LastRole(t *testing.T) {
	c := &Conversation{}

	// 空历史返回 ""
	if r := c.LastRole(); r != "" {
		t.Errorf("空历史期望 \"\"，实际 %q", r)
	}

	// AddUser 后返回 "user"
	c.AddUser("hello")
	if r := c.LastRole(); r != "user" {
		t.Errorf("AddUser 后期望 user，实际 %q", r)
	}

	// AddAssistant 后返回 "assistant"
	c.AddAssistant("hi")
	if r := c.LastRole(); r != "assistant" {
		t.Errorf("AddAssistant 后期望 assistant，实际 %q", r)
	}

	// AddToolResults 后返回 "tool"
	c.AddToolResults(nil)
	if r := c.LastRole(); r != "tool" {
		t.Errorf("AddToolResults 后期望 tool，实际 %q", r)
	}
}
