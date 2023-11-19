"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { Message } from "../../types";

export interface ChatMessageProps {
  message: Message;
}

const ChatMessage = ({ message }: ChatMessageProps) => (
  <div className="message py-2 my-1">
    <strong>{message.sender_type}</strong>
    <br />
    <span>{message.text}</span>
  </div>
);

export default ChatMessage;
