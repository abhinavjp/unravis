import { mount } from 'svelte';
import './lib/updateCheck';
import App from './App.svelte';
import './app.css';
import { markPageVisibility } from './lib/visibility';

markPageVisibility();

mount(App, { target: document.getElementById('app')! });
