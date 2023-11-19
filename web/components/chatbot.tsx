"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { useState, useCallback } from "react";

import SidePanel from "./side_panel/side_panel";
import ChatWindow from "./chat_window/chat_window";

export default function ChatBot() {
  const [chatId, setChatId] = useState(-1);
  return (
    <div className="vh-100 d-flex">
      <div className="col-3">
        <SidePanel chatId={chatId} onChatSelected={setChatId} />
      </div>
      <div className="col-9">
        <ChatWindow chatId={chatId} onChatCreated={setChatId} />
      </div>
    </div>
  );
}
