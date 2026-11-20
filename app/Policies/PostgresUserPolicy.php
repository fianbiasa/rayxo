<?php

namespace App\Policies;

use App\Models\PostgresUser;
use App\Models\User;
use Illuminate\Auth\Access\Response;

class PostgresUserPolicy
{
    /**
     * Accounts may only change their own PostgreSQL roles. Other accounts'
     * roles are reported as missing so their existence is not revealed.
     */
    public function update(User $user, PostgresUser $postgresUser): Response
    {
        return $postgresUser->user_id === $user->id
            ? Response::allow()
            : Response::denyAsNotFound();
    }

    /**
     * Accounts may only delete their own PostgreSQL roles. Other accounts'
     * roles are reported as missing so their existence is not revealed.
     */
    public function delete(User $user, PostgresUser $postgresUser): Response
    {
        return $this->update($user, $postgresUser);
    }
}
