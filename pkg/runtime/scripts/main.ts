import { mountRuntime } from "./RuntimeApp";
import "../styles/public-runtime.css";

const root = document.getElementById("root");
if (!root) throw new Error("Public runtime root element is missing");

// The prerendered page is the live mount tree. ABI modules can hydrate this
// markup when their framework supports it; other modules keep their existing
// mount behavior. Avoid replacing the visible server document with a duplicate
// hidden host, which would delay LCP and duplicate landmarks during startup.
document.getElementById("engine-live-root")?.remove();

mountRuntime(root);
