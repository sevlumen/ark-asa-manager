import React from "react";
import { createRoot } from "react-dom/client";
import "./style.css";

function App() {
  return <main className="shell">
    <p className="eyebrow">ARK ASA PLATFORM</p>
    <h1>Server operations, in one place.</h1>
    <p className="muted">The control plane is running in Docker. Connect an agent to manage ARK instances.</p>
    <section className="card"><span className="dot" /> Control plane health <strong>Ready to connect</strong></section>
  </main>;
}

createRoot(document.getElementById("root")!).render(<React.StrictMode><App /></React.StrictMode>);
