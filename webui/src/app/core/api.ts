import { inject, Injectable } from '@angular/core';
import { Router } from '@angular/router';
import {
  CallOptions,
  Code,
  ConnectError,
  createClient,
  createContextKey,
  createContextValues,
  Interceptor,
  Transport,
} from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { Certificate as CertificateService } from '../../gen/flock/services/cert/v1/cert_pb';
import { Backend as BackendService } from '../../gen/flock/services/backend/v1/backend_pb';
import { ListFilter } from '../../gen/flock/services/core/v1/filter_pb';
import { Info } from '../../gen/flock/services/core/v1/info_pb';
import { Secret as SecretService } from '../../gen/flock/services/secret/v1/secret_pb';
import { Instance as InstanceService } from '../../gen/flock/services/instance/v1/instance_pb';
import { Login } from '../../gen/flock/services/login/v1/login_pb';
import { Lookup } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { Permission } from '../../gen/flock/services/permission/v1/permission_pb';
import { VIP as VipService } from '../../gen/flock/services/vip/v1/vip_pb';
import { VirtualServer as VsService } from '../../gen/flock/services/vs/v1/virtual_server_pb';
import { Zone as ZoneService } from '../../gen/flock/services/zone/v1/zone_pb';
import { Notifier, Progress } from './notifier';
import { KindName, Resource } from './resources';
import { authHeaders, Session } from './session';

// silentErrors marks calls whose errors the caller shows itself, or which may fail without bothering the user
export const silentErrors = createContextKey(false);
export const silentCall: CallOptions = {
  contextValues: createContextValues().set(silentErrors, true),
};

export interface ListPage {
  limit: number;
  offset: number;
  filters: ListFilter[];
}

@Injectable({ providedIn: 'root' })
export class Api {
  // login calls go without the session: they make it, and the login page shows their errors
  readonly login = createClient(Login, this.makeTransport([this.trackProgress()]));
  private session = inject(Session);
  private progress = inject(Progress);
  private notifier = inject(Notifier);
  private router = inject(Router);
  private transport = this.makeTransport([
    this.trackProgress(),
    this.reportErrors(),
    this.authenticate(),
  ]);
  readonly info = createClient(Info, this.transport);
  readonly vs = createClient(VsService, this.transport);
  readonly vip = createClient(VipService, this.transport);
  readonly backend = createClient(BackendService, this.transport);
  readonly certificate = createClient(CertificateService, this.transport);
  readonly secret = createClient(SecretService, this.transport);
  readonly instance = createClient(InstanceService, this.transport);
  readonly zone = createClient(ZoneService, this.transport);
  readonly lookup = createClient(Lookup, this.transport);
  readonly permission = createClient(Permission, this.transport);

  // list lists one page of any kind of resource, for the screens showing several kinds
  async list(kind: KindName, page: ListPage): Promise<Resource[]> {
    switch (kind) {
      case 'vs':
        return (await this.vs.listVS(page)).vs;
      case 'vip':
        return (await this.vip.listVIP(page)).vip;
      case 'backend':
        return (await this.backend.listBackend(page)).backends;
      case 'certificate':
        return (await this.certificate.listCertificate(page)).certificates;
      case 'secret':
        return (await this.secret.listSecret(page)).secrets;
      case 'instance':
        return (await this.instance.listInstance(page)).instances;
      case 'zone':
        return (await this.zone.listZone(page)).zones;
      case 'endpoint':
        throw new Error('endpoints are not listed, they are statuses of a backend');
    }
  }

  private makeTransport(interceptors: Interceptor[]): Transport {
    // the dashboard is served by the controller: the api is on the same origin
    return createConnectTransport({
      baseUrl: window.location.origin,
      useBinaryFormat: true,
      interceptors,
    });
  }

  private trackProgress(): Interceptor {
    return (next) => async (req) => {
      // a stream stays open as long as its page, it is not something the user waits for
      if (req.stream) {
        return next(req);
      }
      this.progress.start();
      try {
        return await next(req);
      } finally {
        this.progress.done();
      }
    };
  }

  private reportErrors(): Interceptor {
    return (next) => async (req) => {
      try {
        const resp = await next(req);
        this.notifier.success(req.method.name);
        return resp;
      } catch (err) {
        const connectErr = ConnectError.from(err);
        if (connectErr.code === Code.Canceled) {
          throw err;
        }
        if (connectErr.code === Code.Unauthenticated) {
          this.session.expire(this.router.url);
          this.router.navigate(['/login']);
          throw err;
        }
        if (!req.contextValues.get(silentErrors)) {
          this.notifier.error(connectErr);
        }
        throw err;
      }
    };
  }

  private authenticate(): Interceptor {
    return (next) => async (req) => {
      const bearer = await this.session.bearer();
      // the cli server proxying the api adds the token itself
      if (bearer === '') {
        return next(req);
      }
      const headers = authHeaders(bearer);
      for (const [name, value] of Object.entries(headers)) {
        req.header.set(name, value);
      }
      return next(req);
    };
  }
}
