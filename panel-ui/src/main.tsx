import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource/inter/400.css";
import "@fontsource/inter/500.css";
import "@fontsource/inter/600.css";
import "@fontsource/inter/700.css";
import "antd/dist/reset.css";
import "./global.css";

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
