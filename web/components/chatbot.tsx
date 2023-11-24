"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { useState, useCallback, useEffect } from "react";

import SidePanel from "./side_panel/side_panel";
import ChatWindow from "./chat_window/chat_window";
import AuthModal from "./auth/auth_modal";
import { useToken, useAuthUser } from "../hooks/api";

export default function ChatBot() {
  const [chatId, setChatId] = useState(-1);

  const [token, setToken] = useToken();
  const { user, getAuthUser, loading: userLoading } = useAuthUser(token);
  useEffect(() => {
    getAuthUser();
    if (!token) {
      setChatId(-1);
    }
  }, [token, getAuthUser]);
  const authModalShowing = !user && !userLoading;

  return (
    <div className="container-fluid vh-100 d-flex">
      <div
        style={{
          transition: "opacity 0.3s ease",
          opacity: authModalShowing ? 1 : 0,
        }}
      >
        <AuthModal
          showing={authModalShowing}
          token={token}
          setToken={setToken}
        />
      </div>
      <div className="col-3 col-md-4 col-lg-3">
        <SidePanel
          user={user}
          token={token}
          setToken={setToken}
          chatId={chatId}
          onChatSelected={setChatId}
        />
      </div>

      <div className="col-9 col-md-8 col-lg-9">
        <ChatWindow
          token={token}
          setToken={setToken}
          chatId={chatId}
          onChatCreated={setChatId}
        />
      </div>
    </div>
  );
}
