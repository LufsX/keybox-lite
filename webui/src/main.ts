import { mount } from "svelte"
import App from "./App.svelte"
import "./app.css"
import { initializeEntryRoute } from "./lib/entryRoute"

initializeEntryRoute()
mount(App, { target: document.getElementById("app")! })
