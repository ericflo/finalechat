"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { Message } from "./chat_window";

export interface ChatMessageProps {
  message: Message;
}

const ChatMessage = ({ message }: ChatMessageProps) => (
  <div className="message py-2 my-1">
    <strong>{message.sender}</strong>
    <br />
    <span>{message.content}</span>
  </div>
);

export default ChatMessage;
