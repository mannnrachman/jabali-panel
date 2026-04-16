import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@refinedev/antd/dist/reset.css";

import App from "./App";

const rootEl = document.getElementById("root");
if (!rootEl) {
  // This would mean index.html was modified incorrectly. Fail loud so
  // nobody ships a page that renders nothing.
  throw new Error("root element missing; check index.html");
}

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
