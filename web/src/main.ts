import { mount } from 'svelte';
import './lib/updateCheck';
import App from './App.svelte';
import './app.css';

mount(App, { target: document.getElementById('app')! });
