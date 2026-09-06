import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { Login } from "./Login";
import "./styles.css";
import "./auth.css";

const root = document.getElementById("root");

if (!root) {
  throw new Error("root element is missing");
}

createRoot(root).render(
  <StrictMode>
    {window.location.pathname === "/login" ? <Login /> : <App />}
  </StrictMode>,
);
