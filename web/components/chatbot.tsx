"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import SidePanel from "./side_panel/side_panel";
import ChatWindow from "./chat_window/chat_window";

export default function ChatBot() {
  return (
    <div className="vh-100 d-flex">
      <div className="col-3">
        <SidePanel />
      </div>
      <div className="col-9">
        <ChatWindow />
      </div>
    </div>
  );
}
