"use client";
import { useCallback, useState } from "react";
import { useLogin } from "../../hooks/api";

export interface LoginProps {
  showing: boolean;
  token: string;
  setToken: (token: string) => void;
  setIsLogin: (isLogin: boolean) => void;
}

const Login = ({ showing, token, setToken, setIsLogin }: LoginProps) => {
  const { login, loading, error } = useLogin(setToken);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const handleSubmit = useCallback(
    (e) => {
      e.preventDefault();
      login(email, password);
    },
    [email, password, login]
  );

  return (
    <div className={"modal d-block" + (showing ? " show" : "hide")}>
      <div className="modal-dialog">
        <div className="modal-content">
          <div className="modal-header">
            <h5 className="modal-title">Login</h5>
          </div>
          <div className="modal-body">
            <form onSubmit={handleSubmit}>
              {error && (
                <div className="alert alert-danger" role="alert">
                  {error}
                </div>
              )}
              <div className="mb-3">
                <label htmlFor="email" className="form-label">
                  E-Mail
                </label>
                <input
                  id="email"
                  type="email"
                  className="form-control"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  aria-label="E-Mail"
                />
              </div>
              <div className="mb-3">
                <label htmlFor="password" className="form-label">
                  Password
                </label>
                <input
                  id="password"
                  type="password"
                  className="form-control"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  aria-label="Password"
                />
              </div>
              <button
                type="submit"
                className="btn btn-primary"
                disabled={loading}
              >
                {loading ? "Loading..." : "Login"}
              </button>
            </form>
          </div>
          <div className="modal-footer">
            <button
              type="button"
              className="btn btn-light"
              onClick={() => setIsLogin(false)}
            >
              Register Instead
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};

export default Login;
