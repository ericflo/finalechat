"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useState, useEffect, useCallback } from "react";
import { Message } from "../../types";
import MessageList from "./message_list";
import MessageInput from "./message_input"; // Import the new component

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

const ChatWindow: React.FC = () => {
  const [messages, setMessages] = useState<Message[]>([]);

  const loadInitialMessages = useCallback(async () => {
    const newMessages = await fetchMessages();
    setMessages(newMessages);
  }, []);

  useEffect(() => {
    loadInitialMessages();
  }, [loadInitialMessages]);

  const addMessage = useCallback((content: string) => {
    setMessages((prevMessages) => [
      ...prevMessages,
      { sender: "User", content },
    ]);
  }, []);

  return (
    <div className="d-flex flex-column h-100">
      <MessageList messages={messages} />
      <MessageInput onSendMessage={addMessage} />
    </div>
  );
};

export default ChatWindow;
