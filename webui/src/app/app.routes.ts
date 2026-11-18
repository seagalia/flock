import { Routes } from '@angular/router';
import { loggedInGuard } from './core/session';
import { Callback } from './login/callback';
import { Login } from './login/login';
import { Shell } from './shell/shell';

// pages load on first visit: the terminal, certificate parser and diff only matter to their page
export const routes: Routes = [
  { path: 'login', component: Login, title: 'Flock · Login' },
  { path: 'callback', component: Callback, title: 'Flock · Signing in' },
  {
    path: '',
    component: Shell,
    canActivate: [loggedInGuard],
    children: [
      {
        path: '',
        loadComponent: () => import('./pages/overview').then((m) => m.Overview),
        title: 'Flock · Overview',
      },
      {
        path: 'virtualservers',
        loadComponent: () => import('./pages/virtual-servers').then((m) => m.VirtualServers),
        title: 'Flock · Virtual Servers',
      },
      {
        path: 'backends',
        loadComponent: () => import('./pages/backends').then((m) => m.Backends),
        title: 'Flock · Backends',
      },
      {
        path: 'vips',
        loadComponent: () => import('./pages/vips').then((m) => m.Vips),
        title: 'Flock · VIPs',
      },
      {
        path: 'secrets',
        loadComponent: () => import('./pages/secrets').then((m) => m.Secrets),
        title: 'Flock · Secrets',
      },
      {
        path: 'certificates',
        loadComponent: () => import('./pages/certificates').then((m) => m.Certificates),
        title: 'Flock · Certificates',
      },
      {
        path: 'instances',
        loadComponent: () => import('./pages/instances').then((m) => m.Instances),
        title: 'Flock · Instances',
      },
      {
        path: 'zones',
        loadComponent: () => import('./pages/zones').then((m) => m.Zones),
        title: 'Flock · Zones',
      },
      {
        path: 'status',
        loadComponent: () => import('./pages/status').then((m) => m.Status),
        title: 'Flock · Status',
      },
      {
        path: 'monitors',
        loadComponent: () => import('./pages/monitors').then((m) => m.Monitors),
        title: 'Flock · Monitors',
      },
    ],
  },
  { path: '**', redirectTo: '' },
];
