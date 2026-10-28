import { mount } from 'svelte';
import App from './App.svelte';
import './setup.css';

mount(App, { target: document.getElementById('app')! });
