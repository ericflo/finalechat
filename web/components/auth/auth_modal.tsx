"use client";
import { useState } from "react";

import Login from "./login";
import Register from "./register";

export interface AuthModalProps {
  showing: boolean;
  token: string;
  setToken: (token: string) => void;
}

const AuthModal = ({ showing, token, setToken }: AuthModalProps) => {
  const [isLogin, setIsLogin] = useState(true);
  const props = { showing, token, setToken, setIsLogin };
  return isLogin ? <Login {...props} /> : <Register {...props} />;
};

export default AuthModal;
