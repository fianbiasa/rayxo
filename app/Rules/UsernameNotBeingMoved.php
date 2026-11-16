<?php

namespace App\Rules;

use App\Models\AccountImport;
use App\Models\AccountTransfer;
use Closure;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Translation\PotentiallyTranslatedString;

/**
 * Refuse a username that an account move (a transfer from another panel, or a
 * cPanel/DirectAdmin import) is still using. A move checks the name is free
 * when it starts but creates the account only at the end, so without this a
 * new account could take the name in between and receive the moved backup.
 */
class UsernameNotBeingMoved implements ValidationRule
{
    /**
     * @param  Closure(string, ?string=): PotentiallyTranslatedString  $fail
     */
    public function validate(string $attribute, mixed $value, Closure $fail): void
    {
        if (! is_string($value)) {
            return;
        }

        $beingMoved = AccountTransfer::query()->where('username', $value)->where('status', '!=', AccountTransfer::STATUS_FAILED)->exists()
            || AccountImport::query()->where('username', $value)->where('status', '!=', AccountImport::STATUS_FAILED)->exists();

        if ($beingMoved) {
            $fail('An account with this username is being moved in; wait for it to finish or pick another name.')->translate();
        }
    }
}
