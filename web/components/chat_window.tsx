"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useState, useEffect, useCallback, useRef, use } from "react";
import ChatMessage from "./chat_message";

export type Message = {
  sender: string;
  content: string;
};

async function fetchMessages(): Promise<Message[]> {
  // Replace with actual data fetching logic
  return [
    { sender: "User", content: "Hello!" },
    { sender: "Agent", content: "Hi, how can I help you?" },
    { sender: "User", content: "I have a question about my order." },
    { sender: "Agent", content: "Sure, what's your order number?" },
    { sender: "User", content: "123456789" },
    { sender: "Agent", content: "Thanks, let me check on that." },
    { sender: "Agent", content: "It looks like your order is on the way!" },
    { sender: "Agent", content: "It should arrive by tomorrow." },
    { sender: "User", content: "Great, thanks!" },
  ];
}

// This component is the main chat window that displays the messages between the user and
// an assistant. It should produce a center column that displays the messages, with a small left
// column that displays the user's profile, and the right column that displays the user or agent
// name in bold, and then the chat history. The chat history should be scrollable.
// At the bottom is a sticky footer that contains a text input and a send button, which should
// send the message to the assistant when clicked. The text input should be cleared after the
// message is sent. Also, hitting enter while typing should send the input, but shift+enter should
// insert a newline into the input.
export default function ChatWindow() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [newMessage, setNewMessage] = useState<string>("");
  const chatHistoryRef = useRef<HTMLDivElement>(null);

  const loadInitialMessages = useCallback(async () => {
    // Replace with actual data fetching logic
    const newMessages = await fetchMessages();
    setMessages(newMessages);
  }, []);
  useEffect(() => {
    loadInitialMessages();
  }, [loadInitialMessages]);

  // Add a new message to the chat
  const addMessage = useCallback((content: string) => {
    if (!content.trim()) return;
    setMessages((prevMessages) => [
      ...prevMessages,
      { sender: "User", content },
    ]);
    setNewMessage("");
  }, []);

  const handleSendClick = useCallback(
    () => addMessage(newMessage),
    [newMessage, addMessage]
  );

  useEffect(() => {
    // Scroll to the bottom of the chat history whenever messages update
    if (chatHistoryRef.current) {
      chatHistoryRef.current.scrollTop = chatHistoryRef.current.scrollHeight;
    }
  }, [messages]);

  const handleTextareaChange = useCallback(
    (event: React.ChangeEvent<HTMLTextAreaElement>) => {
      setNewMessage(event.target.value);
    },
    []
  );

  // Handle Enter to send, Shift+Enter for newline
  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        addMessage(newMessage);
      }
    },
    [newMessage, addMessage]
  );

  return (
    <div className="d-flex flex-column h-100">
      <div className="flex-grow-1 overflow-auto" ref={chatHistoryRef}>
        <div className="d-flex justify-content-center w-100">
          <div
            className="chat-history"
            style={{ maxWidth: "800px", width: "100%" }}
          >
            {messages.map((message, index) => (
              <ChatMessage key={index} message={message} />
            ))}
          </div>
        </div>
      </div>
      <div className="mt-auto p-4">
        <div className="input-group">
          <textarea
            className="form-control"
            placeholder="Type a message..."
            value={newMessage}
            onChange={handleTextareaChange}
            onKeyDown={handleKeyDown}
            style={{ height: "50px", resize: "none" }}
          />
          <button className="btn btn-primary" onClick={handleSendClick}>
            Send
          </button>
        </div>
      </div>
    </div>
  );
}
