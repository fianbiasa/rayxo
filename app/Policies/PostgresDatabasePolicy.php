<?php

namespace App\Policies;

use App\Models\PostgresDatabase;
use App\Models\User;
use Illuminate\Auth\Access\Response;

class PostgresDatabasePolicy
{
    /**
     * Accounts may only export or import into their own databases. Other
     * accounts' databases are reported as missing.
     */
    public function update(User $user, PostgresDatabase $postgresDatabase): Response
    {
        return $postgresDatabase->user_id === $user->id
            ? Response::allow()
            : Response::denyAsNotFound();
    }

    /**
     * Accounts may only delete their own databases. Other accounts'
     * databases are reported as missing so their existence is not revealed.
     */
    public function delete(User $user, PostgresDatabase $postgresDatabase): Response
    {
        return $this->update($user, $postgresDatabase);
    }
}
