import { COMMA, ENTER, SPACE } from '@angular/cdk/keycodes';
import { Location } from '@angular/common';
import { Component, Injector, Type } from '@angular/core';
import { AbstractControl, FormControl, FormGroup } from '@angular/forms';
import { MatChipInputEvent } from '@angular/material/chips';
import { ActivatedRoute } from '@angular/router';
import { BehaviorSubject, Observable, switchMap, take } from 'rxjs';
import {
  AddGoogleProviderRequest as AdminAddGoogleProviderRequest,
  GetProviderByIDRequest as AdminGetProviderByIDRequest,
  UpdateGoogleProviderRequest as AdminUpdateGoogleProviderRequest,
} from 'src/app/proto/generated/zitadel/admin_pb';
import {IDPOwnerType, Options, Provider} from 'src/app/proto/generated/zitadel/idp_pb';
import {
  AddGoogleProviderRequest as MgmtAddGoogleProviderRequest,
  GetProviderByIDRequest as MgmtGetProviderByIDRequest,
  UpdateGoogleProviderRequest as MgmtUpdateGoogleProviderRequest,
} from 'src/app/proto/generated/zitadel/management_pb';
import { AdminService } from 'src/app/services/admin.service';
import { Breadcrumb, BreadcrumbService, BreadcrumbType } from 'src/app/services/breadcrumb.service';
import { GrpcAuthService } from 'src/app/services/grpc-auth.service';
import { ManagementService } from 'src/app/services/mgmt.service';
import { ToastService } from 'src/app/services/toast.service';
import { requiredValidator } from '../../form-field/validators/validators';

import { PolicyComponentServiceType } from '../../policies/policy-component-types.enum';
import { MatDialog } from '@angular/material/dialog';
import { ProviderNextService } from '../provider-next/provider-next.service';
import {CopyUrl} from "../provider-next/provider-next.component";

@Component({
  selector: 'cnsl-provider-google',
  templateUrl: './provider-google.component.html',
})
export class ProviderGoogleComponent {
  public showOptional: boolean = false;
  public options: Options = new Options().setIsCreationAllowed(true).setIsLinkingAllowed(true);
  public id: string | null = '';
  public serviceType: PolicyComponentServiceType = PolicyComponentServiceType.MGMT;
  private service!: ManagementService | AdminService;

  public readonly separatorKeysCodes: number[] = [ENTER, COMMA, SPACE];

  public form!: FormGroup;

  public loading: boolean = false;

  public provider?: Provider.AsObject;
  public updateClientSecret: boolean = false;

  public autofillLink$ = new BehaviorSubject<string>('');
  public activateLink$ = new BehaviorSubject<string>('');
  public isActive$ = new BehaviorSubject<boolean>(false)
  public expandWhatNow$: BehaviorSubject<boolean> = new BehaviorSubject<boolean>(false);
  public copyUrls$: Observable<CopyUrl[]> = this.nextSvc.callbackUrls();
  public configureProvider$ = new BehaviorSubject<boolean>(false);
  public isInstance: boolean = false;

  constructor(
    private authService: GrpcAuthService,
    private route: ActivatedRoute,
    private toast: ToastService,
    private injector: Injector,
    private _location: Location,
    private breadcrumbService: BreadcrumbService,
    private dialog: MatDialog,
    private nextSvc: ProviderNextService,
  ) {
    this.form = new FormGroup({
      name: new FormControl('', []),
      clientId: new FormControl('', [requiredValidator]),
      clientSecret: new FormControl('', [requiredValidator]),
      scopesList: new FormControl(['openid', 'profile', 'email'], []),
    });
    this.authService
      .isAllowed(
        this.serviceType === PolicyComponentServiceType.ADMIN
          ? ['iam.idp.write']
          : this.serviceType === PolicyComponentServiceType.MGMT
            ? ['org.idp.write']
            : [],
      )
      .pipe(take(1))
      .subscribe((allowed) => {
        if (allowed) {
          this.form.enable();
        } else {
          this.form.disable();
        }
      });

    this.route.data.pipe(take(1)).subscribe((data) => {
      this.serviceType = data['serviceType'];

      switch (this.serviceType) {
        case PolicyComponentServiceType.MGMT:
          this.service = this.injector.get(ManagementService as Type<ManagementService>);

          const bread: Breadcrumb = {
            type: BreadcrumbType.ORG,
            routerLink: ['/org'],
          };

          this.breadcrumbService.setBreadcrumb([bread]);
          break;
        case PolicyComponentServiceType.ADMIN:
          this.isInstance = true;
          this.service = this.injector.get(AdminService as Type<AdminService>);

          const iamBread = new Breadcrumb({
            type: BreadcrumbType.ORG,
            name: 'Instance',
            routerLink: ['/instance'],
          });
          this.breadcrumbService.setBreadcrumb([iamBread]);
          break;
      }
      this.id = this.route.snapshot.paramMap.get('id');
      if (this.id) {
        this.showAutofillLink();
        this.clientSecret?.setValidators([]);
        this.getData(this.id);
      } else {
        this.expandWhatNow$.next(true);
        this.configureProvider$.next(true);
      }
    });
  }

  private showAutofillLink(): void {
    this.autofillLink$.next('https://zitadel.com/docs/guides/integrate/identity-providers/additional-information');
  }

  private getData(id: string): void {
    const req =
      this.serviceType === PolicyComponentServiceType.ADMIN
        ? new AdminGetProviderByIDRequest()
        : new MgmtGetProviderByIDRequest();
    req.setId(id);
    this.service
      .getProviderByID(req)
      .then((resp) => {
        this.provider = resp.idp;
        this.loading = false;
        if (this.provider?.config?.google) {
          this.form.patchValue(this.provider.config.google);
          this.name?.setValue(this.provider.name);
        }
      })
      .catch((error) => {
        this.toast.showError(error);
        this.loading = false;
      });
    this.service.getLoginPolicy()
      .then((policy) => {
        this.isActive$.next(!!policy.policy?.idpsList.find(idp => idp.idpId === this.id));
        this.setActivateable(this.isActive$.value ? '' : id);
      })
      .catch((error) => {
        this.toast.showError(error);
      });
  }

  public submitForm(): void {
    this.provider ? this.updateGoogleProvider() : this.addGoogleProvider();
  }

  public addGoogleProvider(): void {
    const req =
      this.serviceType === PolicyComponentServiceType.MGMT
        ? new MgmtAddGoogleProviderRequest()
        : new AdminAddGoogleProviderRequest();

    req.setName(this.name?.value);
    req.setClientId(this.clientId?.value);
    req.setClientSecret(this.clientSecret?.value);
    req.setScopesList(this.scopesList?.value);
    req.setProviderOptions(this.options);

    this.loading = true;
    this.service
      .addGoogleProvider(req)
      .then((addedIDP) => {
        this.showAutofillLink();
        this.setActivateable(addedIDP.id);
        this.configureProvider$.next(false);
        this.loading = false;
      })
      .catch((error) => {
        this.toast.showError(error);
        this.loading = false;
      });
  }

  public activate() {
    this.service.addIDPToLoginPolicy(this.id!, this.serviceType === PolicyComponentServiceType.ADMIN ? IDPOwnerType.IDP_OWNER_TYPE_SYSTEM : IDPOwnerType.IDP_OWNER_TYPE_ORG).then(() => {
      this.toast.showInfo('POLICY.TOAST.ADDIDP', true);
      this.isActive$.next(true);
      this.setActivateable('');
    });
  }

  public updateGoogleProvider(): void {
    if (this.provider) {
      if (this.serviceType === PolicyComponentServiceType.MGMT) {
        const req = new MgmtUpdateGoogleProviderRequest();
        req.setId(this.provider.id);
        req.setName(this.name?.value);
        req.setClientId(this.clientId?.value);
        req.setScopesList(this.scopesList?.value);
        req.setProviderOptions(this.options);

        if (this.updateClientSecret) {
          req.setClientSecret(this.clientSecret?.value);
        }

        this.loading = true;
        (this.service as ManagementService)
          .updateGoogleProvider(req)
          .then((idp) => {
            setTimeout(() => {
              this.loading = false;
              this.close();
            }, 2000);
          })
          .catch((error) => {
            this.toast.showError(error);
            this.loading = false;
          });
      } else if (PolicyComponentServiceType.ADMIN) {
        const req = new AdminUpdateGoogleProviderRequest();
        req.setId(this.provider.id);
        req.setName(this.name?.value);
        req.setClientId(this.clientId?.value);
        req.setScopesList(this.scopesList?.value);
        req.setProviderOptions(this.options);

        if (this.updateClientSecret) {
          req.setClientSecret(this.clientSecret?.value);
        }

        this.loading = true;
        (this.service as AdminService)
          .updateGoogleProvider(req)
          .then((idp) => {
            setTimeout(() => {
              this.loading = false;
              this.close();
            }, 2000);
          })
          .catch((error) => {
            this.loading = false;
            this.toast.showError(error);
          });
      }
    }
  }

  public close(): void {
    this._location.back();
  }

  public addScope(event: MatChipInputEvent): void {
    const input = event.chipInput?.inputElement;
    const value = event.value.trim();

    if (value !== '') {
      if (this.scopesList?.value) {
        this.scopesList.value.push(value);
        if (input) {
          input.value = '';
        }
      }
    }
  }

  public removeScope(uri: string): void {
    if (this.scopesList?.value) {
      const index = this.scopesList.value.indexOf(uri);

      if (index !== undefined && index >= 0) {
        this.scopesList.value.splice(index, 1);
      }
    }
  }

  private setActivateable(id: string) {
    this.activateLink$.next(!id ? '' : 'https://zitadel.com/docs/guides/integrate/identity-providers/google#activate-idp');
    if (id) {
      this.expandWhatNow$.next(true);
      this.id = id;
    }
  }

  public get name(): AbstractControl | null {
    return this.form.get('name');
  }

  public get clientId(): AbstractControl | null {
    return this.form.get('clientId');
  }

  public get clientSecret(): AbstractControl | null {
    return this.form.get('clientSecret');
  }

  public get scopesList(): AbstractControl | null {
    return this.form.get('scopesList');
  }
}
