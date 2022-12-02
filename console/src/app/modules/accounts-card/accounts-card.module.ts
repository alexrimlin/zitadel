import { CommonModule } from '@angular/common';
import { NgModule } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { RouterModule } from '@angular/router';
import { TranslateModule } from '@ngx-translate/core';

import { AvatarModule } from '../avatar/avatar.module';
import { AccountsCardComponent } from './accounts-card.component';

@NgModule({
  declarations: [AccountsCardComponent],
  imports: [
    CommonModule,
    MatIconModule,
    MatButtonModule,
    MatProgressSpinnerModule,
    RouterModule,
    AvatarModule,
    TranslateModule,
  ],
  exports: [AccountsCardComponent],
})
export class AccountsCardModule {}
